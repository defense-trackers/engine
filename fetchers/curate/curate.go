// Package curate emits records from a human-maintained JSON array under
// engine/curated/. It is the "moat column" path: the matrices and lists only a
// domain expert can write (NIPR tool matrix, Blue UAS parts, TAK SDK-compat,
// curated events). Curated edits diff like any source, so they land in the
// changelog with the same hash-chained provenance as scraped data.
package curate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"engine/internal/core"
)

type Curate struct{ Dir string }

func New(dir string) *Curate { return &Curate{Dir: dir} }

func (c *Curate) Fetch(ct core.Contract, cacheDir string) ([]core.Record, error) {
	file := ct.CuratedFile
	if file == "" {
		file = ct.ID + ".json"
	}
	path := filepath.Join(c.Dir, file)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("curate: cannot read %s (%v)", path, err)
	}
	return Parse(b, ct)
}

// Parse converts a JSON array of flat objects into records.
func Parse(b []byte, ct core.Contract) ([]core.Record, error) {
	var rows []map[string]interface{}
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("curate: expected a JSON array of objects (%v)", err)
	}
	keyField := ct.KeyField
	if keyField == "" {
		keyField = "id"
	}
	var records []core.Record
	seen := map[string]bool{}
	for i, row := range rows {
		key := str(row[keyField])
		if key == "" {
			key = str(row["key"])
		}
		if key == "" {
			key = fmt.Sprintf("row-%d", i)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		fields := map[string]string{}
		for k, v := range row {
			fields[k] = str(v)
		}
		if fields["text"] == "" {
			switch {
			case fields["name"] != "":
				fields["text"] = fields["name"]
			case fields["title"] != "":
				fields["text"] = fields["title"]
			default:
				fields["text"] = key
			}
		}
		records = append(records, core.Record{Key: key, Fields: fields})
	}
	return records, nil
}

func str(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return fmt.Sprint(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
