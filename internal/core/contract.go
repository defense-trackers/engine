// Package core implements the tracker engine: contracts, fetch, validate,
// diff, append-only storage with a hash chain, status, and RSS.
//
// Design rules (do not casually break):
//   - stdlib only: rot resistance is the product.
//   - schema_version on every State; new fields optional-only; never repurpose.
//   - the public site renders status.json honestly; the engine never deletes
//     last-good data on failure.
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const SchemaVersion = 1

// Contract is the declarative description of one source. Fetchers are
// generic; everything source-specific lives here so the repair loop can
// regenerate behavior by editing data, not code, wherever possible.
type Contract struct {
	ID           string  `json:"id"`            // unique source id, e.g. "blue-uas"
	Tracker      string  `json:"tracker"`       // tracker slug this source feeds
	Method       string  `json:"method"`        // fetcher registry key: pagediff | api | rss
	URL          string  `json:"url"`
	Enabled      bool    `json:"enabled"`
	CadenceHours int     `json:"cadence_hours"` // expected update interval; sentinel math
	RateLimitMS  int     `json:"rate_limit_ms"` // politeness sleep before this source
	SliceStart   string  `json:"slice_start,omitempty"` // pagediff: begin after this literal
	SliceEnd     string  `json:"slice_end,omitempty"`   // pagediff: stop before this literal
	MinRecords   int     `json:"min_records"`           // invariant: fewer records => quarantine
	MaxDeltaPct  float64 `json:"max_delta_pct"`         // invariant: churn % above this => quarantine

	// --- api method (generic JSON over HTTP) ---
	HTTPMethod string            `json:"http_method,omitempty"` // GET (default) | POST
	Body       string            `json:"body,omitempty"`        // raw JSON body for POST (e.g. USAspending)
	ArrayPath  string            `json:"array_path,omitempty"`  // dot-path to the item array ("" = top-level array)
	KeyField   string            `json:"key_field,omitempty"`   // item field used as the stable record key
	FieldMap   map[string]string `json:"field_map,omitempty"`   // output field -> source dot-path
	Query      map[string]string `json:"query,omitempty"`       // extra query params
	AuthEnv    string            `json:"auth_env,omitempty"`    // env var holding a token/key
	AuthMode   string            `json:"auth_mode,omitempty"`   // "header" (Bearer) | "header:<Name>" (raw value). No query-string auth — keys never go in a URL.
	Paginate   string            `json:"paginate,omitempty"`    // "page" | "link" | "" (single)
	PerPage    int               `json:"per_page,omitempty"`    // page size for paginate=page
	MaxPages   int               `json:"max_pages,omitempty"`   // hard cap on pages fetched
	Exclude     []string `json:"exclude,omitempty"`      // drop items whose JSON contains any of these (case-insensitive)
	ExcludeFields []string `json:"exclude_fields,omitempty"` // restrict the exclude match to these field paths (precision); empty = whole item
	Include      []string `json:"include,omitempty"`       // keep ONLY items containing at least one of these
	IncludeFields []string `json:"include_fields,omitempty"` // restrict the include match to these field paths (precision); empty = whole item
	RecentField  string  `json:"recent_field,omitempty"`  // item date field; with recent_days, drop items older than the cutoff (e.g. pushed_at)
	RecentDays   int     `json:"recent_days,omitempty"`   // freshness window in days for recent_field (0 = no recency filter)
	LimitRecords int     `json:"limit_records,omitempty"` // cap to the first N records after mapping (0 = no cap)

	// --- curate method (maintained JSON file) ---
	CuratedFile string `json:"curated_file,omitempty"` // path under curated/ (defaults to <id>.json)

	// --- source linking ---
	URLTemplate string `json:"url_template,omitempty"` // build a per-record source URL, e.g. "https://x/{id}" ({field} from the item)
	DefaultURL  string `json:"default_url,omitempty"`  // fallback source URL for records that still have none

	// --- output options ---
	EmitICal    bool   `json:"emit_ical,omitempty"`    // also emit feeds/<tracker>.ics
	DateField   string `json:"date_field,omitempty"`   // record field holding a date (ical + display)
	ExpireField string `json:"expire_field,omitempty"` // record field holding a deadline; rows past it are dropped at commit (→ "removed" event)

	Notes string `json:"notes,omitempty"`
}

// Record is one normalized row. Key must be stable across runs for diffing:
// for pagediff it is a content hash; for API sources use the upstream id.
type Record struct {
	Key    string            `json:"key"`
	Source string            `json:"source,omitempty"` // which source produced this row (set by the engine)
	Fields map[string]string `json:"fields"`
}

// State is the current published snapshot for a tracker source.
type State struct {
	Source    string   `json:"source"`
	Tracker   string   `json:"tracker"`
	FetchedAt string   `json:"fetched_at"`
	Schema    int      `json:"schema_version"`
	Records   []Record `json:"records"`
}

// Event is one append-only changelog entry. Prev is the hex sha256 of the
// previous event line (or "genesis"), forming a verifiable chain.
type Event struct {
	TS      string `json:"ts"`
	Source  string `json:"source"`
	Type    string `json:"type"` // added | removed | changed
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Prev    string `json:"prev"`
}

// SourceStatus is what the public site renders. The site never lies: state
// and timestamps here are the single source of truth for freshness badges.
type SourceStatus struct {
	Tracker      string `json:"tracker"`
	State        string `json:"state"` // ok | degraded | quarantined | stale
	LastAttempt  string `json:"last_attempt"`
	LastSuccess  string `json:"last_success,omitempty"`
	CadenceHours int    `json:"cadence_hours"`
	Count        int    `json:"count,omitempty"` // records currently published by this source
	Message      string `json:"message,omitempty"`
}

// LoadContracts reads every *.json in dir, validates required fields, and
// returns contracts sorted by ID for deterministic runs.
func LoadContracts(dir string) ([]Contract, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Contract
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("contract %s: %w", p, err)
		}
		var c Contract
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("contract %s: %w", p, err)
		}
		if c.ID == "" || c.Tracker == "" || c.Method == "" || c.URL == "" {
			return nil, fmt.Errorf("contract %s: id, tracker, method, url are required", p)
		}
		if c.CadenceHours == 0 {
			c.CadenceHours = 24
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
