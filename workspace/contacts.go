package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Sponsor is a real, public DoD office that owns money, requirements, a program,
// or a transition path — the named targets for crossing the second valley. These
// are offices/roles (public org structure), not invented individuals; the current
// incumbent is reached through the listed sanctioned channel.
type Sponsor struct {
	Office    string   `json:"office"`
	Role      string   `json:"role"`      // resource sponsor | program office | requirement owner | innovation cell | transition vehicle | S&T sponsor
	Component string   `json:"component"` // Navy | USMC | Army | USAF | USSF | SOCOM | DoD
	Domains   []string `json:"domains"`
	Channel   string   `json:"channel"` // the sanctioned, non-spam way to engage
	Notes     string   `json:"notes,omitempty"`
}

type SponsorBook struct {
	Sponsors []Sponsor `json:"sponsors"`
}

// LoadSponsors reads the local contacts.json, falling back to the embedded example.
func LoadSponsors(dir string) *SponsorBook {
	var bk SponsorBook
	if b, err := os.ReadFile(filepath.Join(dir, "contacts.json")); err == nil {
		if json.Unmarshal(b, &bk) == nil {
			return &bk
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "contacts.json"), exampleContacts, 0o644)
	_ = json.Unmarshal(exampleContacts, &bk)
	return &bk
}

// Match returns sponsors relevant to an opportunity by component and domain/keyword
// overlap, most relevant first (component match weighted highest).
func (bk *SponsorBook) Match(o *Opportunity, max int) []Sponsor {
	if bk == nil {
		return nil
	}
	ag := strings.ToLower(o.Agency)
	hay := o.Text + " " + strings.ToLower(o.MatchedAsset)
	type scored struct {
		s Sponsor
		n int
	}
	var ranked []scored
	for _, s := range bk.Sponsors {
		n := 0
		comp := strings.ToLower(s.Component)
		if comp != "dod" && (strings.Contains(ag, comp) || strings.Contains(ag, compAlias(comp))) {
			n += 3
		}
		for _, d := range s.Domains {
			if d != "" && strings.Contains(hay, strings.ToLower(d)) {
				n++
			}
		}
		if comp == "dod" && n > 0 {
			n++ // DoD-wide vehicles are broadly applicable when any domain hits
		}
		if n > 0 {
			ranked = append(ranked, scored{s, n})
		}
	}
	// simple insertion sort by score desc (small lists)
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].n > ranked[j-1].n; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	out := []Sponsor{}
	for i := 0; i < len(ranked) && i < max; i++ {
		out = append(out, ranked[i].s)
	}
	return out
}

func compAlias(c string) string {
	switch c {
	case "usaf":
		return "air force"
	case "ussf":
		return "space"
	case "navy":
		return "navy"
	case "usmc":
		return "marine"
	default:
		return c
	}
}
