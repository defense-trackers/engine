package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Fetcher converts one Contract into normalized Records. Implementations
// must be deterministic for identical input bytes — golden tests depend on it.
type Fetcher interface {
	Fetch(c Contract, cacheDir string) ([]Record, error)
}

var registry = map[string]Fetcher{}

// Register installs a fetcher under a method name (called from main).
func Register(method string, f Fetcher) { registry[method] = f }

// RunResult summarizes one source's run for the CLI and workflows.
type RunResult struct {
	Source  string
	State   string
	Added   int
	Removed int
	Changed int
	Err     error
}

// RunAll executes every enabled contract (optionally filtered to one id).
func RunAll(contracts []Contract, outDir, cacheDir, quarantineDir, only string) []RunResult {
	var results []RunResult
	for _, c := range contracts {
		if !c.Enabled || (only != "" && c.ID != only) {
			continue
		}
		if c.RateLimitMS > 0 {
			time.Sleep(time.Duration(c.RateLimitMS) * time.Millisecond)
		}
		results = append(results, runOne(c, outDir, cacheDir, quarantineDir))
	}
	return results
}

func runOne(c Contract, outDir, cacheDir, quarantineDir string) RunResult {
	f, ok := registry[c.Method]
	if !ok {
		err := fmt.Errorf("no fetcher registered for method %q", c.Method)
		now := time.Now().UTC().Format(time.RFC3339)
		setStatus(outDir, c.ID, SourceStatus{Tracker: c.Tracker, State: "degraded",
			LastAttempt: now, CadenceHours: c.CadenceHours, Message: err.Error()})
		return RunResult{Source: c.ID, State: "degraded", Err: err}
	}
	recs, err := f.Fetch(c, cacheDir)
	if err != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		setStatus(outDir, c.ID, SourceStatus{Tracker: c.Tracker, State: "degraded",
			LastAttempt: now, CadenceHours: c.CadenceHours, Message: err.Error()})
		return RunResult{Source: c.ID, State: "degraded", Err: err}
	}
	return CommitRecords(c, recs, outDir, cacheDir, quarantineDir)
}

// CommitRecords runs the publish pipeline for already-obtained records:
// per-source diff against prior state, the validation gate, merge into the
// tracker snapshot, append events, RSS, optional iCal, and status. Shared by
// runOne (fetched records) and synthetic publishers like the deadlines
// aggregator.
func CommitRecords(c Contract, recs []Record, outDir, cacheDir, quarantineDir string) RunResult {
	now := time.Now().UTC().Format(time.RFC3339)
	res := RunResult{Source: c.ID}

	fail := func(state string, err error) RunResult {
		res.State, res.Err = state, err
		setStatus(outDir, c.ID, SourceStatus{
			Tracker: c.Tracker, State: state, LastAttempt: now,
			CadenceHours: c.CadenceHours, Message: err.Error(),
		})
		return res
	}

	for i := range recs {
		recs[i].Source = c.ID
	}

	// A tracker's current.json may aggregate several sources. Diff and validate
	// THIS source against only its own prior records; leave the other sources'
	// records untouched in the merged snapshot.
	full, err := loadCurrent(outDir, c.Tracker)
	if err != nil {
		return fail("degraded", fmt.Errorf("loading current state: %w", err))
	}
	var others, oldForSource []Record
	if full != nil {
		for _, r := range full.Records {
			if r.Source == c.ID {
				oldForSource = append(oldForSource, r)
			} else {
				others = append(others, r)
			}
		}
	}
	oldState := &State{Records: oldForSource}
	if verr := Validate(c, oldState, recs); verr != nil {
		quarantine(quarantineDir, cacheDir, c, recs, verr, now)
		return fail("quarantined", verr)
	}

	events := Diff(oldState, recs, c.ID, now)
	res.Added, res.Removed, res.Changed = countTypes(events)

	merged := make([]Record, 0, len(others)+len(recs))
	merged = append(merged, others...)
	merged = append(merged, recs...)
	st := State{Source: c.Tracker, Tracker: c.Tracker, FetchedAt: now,
		Schema: SchemaVersion, Records: merged}
	if err := writeCurrent(outDir, st); err != nil {
		return fail("degraded", err)
	}
	if err := appendEvents(outDir, c.Tracker, events); err != nil {
		return fail("degraded", err)
	}
	if err := writeRSS(outDir, c.Tracker); err != nil {
		return fail("degraded", err)
	}
	if c.EmitICal {
		if err := WriteICal(outDir, c.Tracker, c.DateField, merged); err != nil {
			return fail("degraded", err)
		}
	}
	res.State = "ok"
	setStatus(outDir, c.ID, SourceStatus{
		Tracker: c.Tracker, State: "ok", LastAttempt: now, LastSuccess: now,
		CadenceHours: c.CadenceHours, Count: len(recs),
		Message: fmt.Sprintf("+%d -%d ~%d", res.Added, res.Removed, res.Changed),
	})
	return res
}

// Validate is the never-lie gate. A failed invariant quarantines the batch
// and keeps last-good live; it never publishes suspect data.
func Validate(c Contract, old *State, recs []Record) error {
	min := c.MinRecords
	if min == 0 {
		min = 1
	}
	if len(recs) < min {
		return fmt.Errorf("invariant: %d records below minimum %d", len(recs), min)
	}
	if old != nil && len(old.Records) > 0 && c.MaxDeltaPct > 0 {
		added, removed, _ := deltaCounts(old.Records, recs)
		pct := float64(added+removed) / float64(len(old.Records)) * 100
		if pct > c.MaxDeltaPct {
			return fmt.Errorf("invariant: %.0f%% churn exceeds max %.0f%% (+%d -%d) — review quarantine before trusting",
				pct, c.MaxDeltaPct, added, removed)
		}
	}
	return nil
}

// Diff computes added/removed/changed events between old state and new records.
func Diff(old *State, recs []Record, source, ts string) []Event {
	oldByKey := map[string]Record{}
	if old != nil {
		for _, r := range old.Records {
			oldByKey[r.Key] = r
		}
	}
	newByKey := map[string]Record{}
	var events []Event
	for _, r := range recs {
		newByKey[r.Key] = r
		prev, existed := oldByKey[r.Key]
		switch {
		case !existed:
			events = append(events, Event{TS: ts, Source: source, Type: "added",
				Key: r.Key, Summary: summarize(r)})
		case canonical(prev.Fields) != canonical(r.Fields):
			events = append(events, Event{TS: ts, Source: source, Type: "changed",
				Key: r.Key, Summary: summarize(r)})
		}
	}
	// Deterministic order for removals.
	var removedKeys []string
	for k := range oldByKey {
		if _, ok := newByKey[k]; !ok {
			removedKeys = append(removedKeys, k)
		}
	}
	sort.Strings(removedKeys)
	for _, k := range removedKeys {
		events = append(events, Event{TS: ts, Source: source, Type: "removed",
			Key: k, Summary: summarize(oldByKey[k])})
	}
	return events
}

func deltaCounts(old, new []Record) (added, removed, changed int) {
	evs := Diff(&State{Records: old}, new, "", "")
	return countTypes(evs)
}

func countTypes(evs []Event) (added, removed, changed int) {
	for _, e := range evs {
		switch e.Type {
		case "added":
			added++
		case "removed":
			removed++
		case "changed":
			changed++
		}
	}
	return
}

func summarize(r Record) string {
	s := r.Fields["text"]
	if s == "" {
		keys := make([]string, 0, len(r.Fields))
		for k := range r.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+r.Fields[k])
		}
		s = strings.Join(parts, " ")
	}
	if len(s) > 140 {
		s = s[:137] + "..."
	}
	return s
}

func canonical(m map[string]string) string {
	b, _ := json.Marshal(m) // Go sorts map keys: deterministic
	return string(b)
}

// ---- storage ----

func trackerDir(outDir, tracker string) string {
	return filepath.Join(outDir, "data", tracker)
}

func loadCurrent(outDir, tracker string) (*State, error) {
	b, err := os.ReadFile(filepath.Join(trackerDir(outDir, tracker), "current.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeCurrent(outDir string, s State) error {
	dir := trackerDir(outDir, s.Tracker)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "current.json"), append(b, '\n'), 0o644)
}

// appendEvents writes events to data/<tracker>/events/<year>.jsonl with each
// line carrying prev = sha256 of the previous line. CHAIN holds the head.
func appendEvents(outDir, tracker string, evs []Event) error {
	if len(evs) == 0 {
		return nil
	}
	dir := filepath.Join(trackerDir(outDir, tracker), "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	chainPath := filepath.Join(trackerDir(outDir, tracker), "CHAIN")
	prev := "genesis"
	if b, err := os.ReadFile(chainPath); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		prev = strings.TrimSpace(string(b))
	}
	year := time.Now().UTC().Format("2006")
	f, err := os.OpenFile(filepath.Join(dir, year+".jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for i := range evs {
		evs[i].Prev = prev
		line, err := json.Marshal(evs[i])
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
		h := sha256.Sum256(line)
		prev = hex.EncodeToString(h[:])
	}
	// Hook point: if SIGNET_CMD is set, also countersign the chain head with
	// signet-sign, e.g. SIGNET_CMD="signet sign --key ..." (non-fatal).
	return os.WriteFile(chainPath, []byte(prev+"\n"), 0o644)
}

// VerifyChain re-derives the hash chain for one tracker (or all when
// tracker == "") and compares against CHAIN. Returns failing trackers.
func VerifyChain(outDir, tracker string) ([]string, error) {
	var trackers []string
	if tracker != "" {
		trackers = []string{tracker}
	} else {
		entries, err := os.ReadDir(filepath.Join(outDir, "data"))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				trackers = append(trackers, e.Name())
			}
		}
	}
	var bad []string
	for _, t := range trackers {
		evDir := filepath.Join(trackerDir(outDir, t), "events")
		files, _ := filepath.Glob(filepath.Join(evDir, "*.jsonl"))
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		running := "genesis"
		valid := true
		for _, fp := range files {
			b, err := os.ReadFile(fp)
			if err != nil {
				valid = false
				break
			}
			for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				if line == "" {
					continue
				}
				var e Event
				if json.Unmarshal([]byte(line), &e) != nil || e.Prev != running {
					valid = false
					break
				}
				h := sha256.Sum256([]byte(line))
				running = hex.EncodeToString(h[:])
			}
			if !valid {
				break
			}
		}
		head, err := os.ReadFile(filepath.Join(trackerDir(outDir, t), "CHAIN"))
		if err != nil || strings.TrimSpace(string(head)) != running {
			valid = false
		}
		if !valid {
			bad = append(bad, t)
		}
	}
	return bad, nil
}

// ---- status ----

func statusPath(outDir string) string { return filepath.Join(outDir, "data", "status.json") }

func readStatus(outDir string) map[string]SourceStatus {
	m := map[string]SourceStatus{}
	if b, err := os.ReadFile(statusPath(outDir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeStatus(outDir string, m map[string]SourceStatus) error {
	if err := os.MkdirAll(filepath.Join(outDir, "data"), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(statusPath(outDir), append(b, '\n'), 0o644)
}

func setStatus(outDir, source string, s SourceStatus) {
	m := readStatus(outDir)
	if old, ok := m[source]; ok && s.LastSuccess == "" {
		s.LastSuccess = old.LastSuccess // failures never erase last-good
	}
	m[source] = s
	_ = writeStatus(outDir, m)
}

// Sentinel marks sources stale when last success exceeds 1.5x cadence and
// returns their ids. The site renders the flip; workflows open issues.
func Sentinel(outDir string) ([]string, error) {
	m := readStatus(outDir)
	var stale []string
	now := time.Now().UTC()
	for id, s := range m {
		if s.State != "ok" {
			continue
		}
		ref := s.LastSuccess
		if ref == "" {
			ref = s.LastAttempt
		}
		t, err := time.Parse(time.RFC3339, ref)
		if err != nil {
			continue
		}
		limit := time.Duration(float64(s.CadenceHours)*1.5) * time.Hour
		if now.Sub(t) > limit {
			s.State = "stale"
			s.Message = fmt.Sprintf("no successful update since %s (cadence %dh)", ref, s.CadenceHours)
			m[id] = s
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	return stale, writeStatus(outDir, m)
}

// ---- quarantine ----

func quarantine(qDir, cacheDir string, c Contract, recs []Record, reason error, ts string) {
	dir := filepath.Join(qDir, c.ID, strings.ReplaceAll(ts, ":", ""))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "reason.txt"), []byte(reason.Error()+"\n"), 0o644)
	if b, err := json.MarshalIndent(recs, "", " "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "records.json"), b, 0o644)
	}
	if body, err := os.ReadFile(BodyCachePath(cacheDir, c.URL)); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "body.html"), body, 0o644)
	}
	if cb, err := json.MarshalIndent(c, "", " "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "contract.json"), cb, 0o644)
	}
}

// ---- rss ----

var xmlEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func writeRSS(outDir, tracker string) error {
	year := time.Now().UTC().Format("2006")
	b, err := os.ReadFile(filepath.Join(trackerDir(outDir, tracker), "events", year+".jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	var items strings.Builder
	for i := len(lines) - 1; i >= 0; i-- { // newest first
		var e Event
		if json.Unmarshal([]byte(lines[i]), &e) != nil {
			continue
		}
		pub := e.TS
		if t, err := time.Parse(time.RFC3339, e.TS); err == nil {
			pub = t.Format(time.RFC1123Z)
		}
		fmt.Fprintf(&items,
			"<item><title>[%s] %s</title><guid isPermaLink=\"false\">%s-%s-%s</guid><pubDate>%s</pubDate></item>\n",
			e.Type, xmlEsc.Replace(e.Summary), tracker, e.Key, xmlEsc.Replace(e.TS), pub)
	}
	feed := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>%s changelog</title>
<link>https://defense-trackers.github.io/tracker.html?t=%s</link>
<description>Change events for %s. Append-only, hash-chained.</description>
%s</channel></rss>
`, xmlEsc.Replace(tracker), tracker, xmlEsc.Replace(tracker), items.String())
	dir := filepath.Join(outDir, "feeds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, tracker+".xml"), []byte(feed), 0o644)
}
