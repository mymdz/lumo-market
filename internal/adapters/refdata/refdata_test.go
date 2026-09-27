package refdata

import (
	"testing"

	"github.com/mymdz/lumo-market/internal/domain"
)

func TestLoad(t *testing.T) {
	rd, err := New().Load()
	if err != nil {
		t.Fatal(err)
	}
	leaves, cats := 0, 0
	var walk func(c *domain.CategorySpec)
	walk = func(c *domain.CategorySpec) {
		cats++
		if c.IsLeaf() {
			leaves++
		}
		for _, ch := range c.Children {
			walk(ch)
		}
	}
	for _, d := range rd.Catalog.Departments {
		walk(d)
	}
	brands := map[string]bool{}
	for _, list := range rd.Catalog.Brands {
		for _, b := range list {
			brands[b.Name] = true
		}
	}
	cities := 0
	for _, c := range rd.Geo.Countries {
		cities += len(c.Cities)
	}
	t.Logf("departments=%d categories=%d leaves=%d brands=%d countries=%d cities=%d",
		len(rd.Catalog.Departments), cats, leaves, len(brands), len(rd.Geo.Countries), cities)
	if leaves < 300 {
		t.Errorf("expected at least 300 leaves, got %d", leaves)
	}
}
