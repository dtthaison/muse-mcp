package voices

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `"name","gender","accent","tone","description"
"Aria","F","American","Warm","Feminine, American, Friendly, Warm"
"Helio","M","American","Smooth","Masculine, American, Confident, Smooth"
"Lan","F","Vietnamese","Bright","Feminine, Vietnamese, Young, Bright"
"Ethereal Star","N","American","Airy","Neutral, American, Otherworldly, Airy"
`

func catalog(t *testing.T) *Catalog {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "voice-list.csv")
	if err := os.WriteFile(src, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Open(filepath.Join(dir, "home", "voices.csv"))
	n, err := c.Import(src)
	if err != nil || n != 4 {
		t.Fatalf("import: %d %v", n, err)
	}
	return c
}

func names(vs []Voice) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return strings.Join(out, ",")
}

func TestSearch(t *testing.T) {
	c := catalog(t)
	for _, tc := range []struct {
		q    Query
		want string
	}{
		{Query{}, "Aria,Helio,Lan,Ethereal Star"},
		{Query{Gender: "female"}, "Aria,Lan"},
		{Query{Gender: "M"}, "Helio"},
		{Query{Gender: "neutral"}, "Ethereal Star"},
		{Query{Accent: "vietnam"}, "Lan"},
		{Query{Tone: "warm"}, "Aria"},
		{Query{Text: "american confident"}, "Helio"},
		{Query{Text: "helio"}, "Helio"},
		{Query{Limit: 2}, "Aria,Helio"},
	} {
		got, total, err := c.Search(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		if names(got) != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.q, names(got), tc.want)
		}
		if tc.q.Limit == 2 && total != 4 {
			t.Errorf("total %d", total)
		}
	}
}

func TestBadGender(t *testing.T) {
	if _, _, err := catalog(t).Search(Query{Gender: "robot"}); err == nil {
		t.Fatal("want error")
	}
}

// Without an import, the catalog built into muse-mcp is used.
func TestBuiltInCatalog(t *testing.T) {
	c := Open(filepath.Join(t.TempDir(), "voices.csv"))
	all, total, err := c.Search(Query{All: true})
	if err != nil || total < 1000 || len(all) != total {
		t.Fatalf("built-in: %d voices, %v", total, err)
	}
	if vs, _, _ := c.Search(Query{Text: "aria"}); len(vs) == 0 || vs[0].Name != "Aria" {
		t.Fatalf("no Aria: %v", names(vs))
	}
	if vs, _, _ := c.Search(Query{Accent: "vietnamese"}); len(vs) == 0 {
		t.Fatal("no Vietnamese-accent voices")
	}
	if c.Imported() {
		t.Fatal("Imported() with no import")
	}
}

// An imported catalog replaces the built-in one.
func TestImportedCatalogWins(t *testing.T) {
	c := catalog(t)
	if _, total, err := c.Search(Query{All: true}); err != nil || total != 4 {
		t.Fatalf("imported: %d voices, %v", total, err)
	}
	if !c.Imported() {
		t.Fatal("Imported() after an import")
	}
}

func TestImportRejectsOtherCSV(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.csv")
	os.WriteFile(src, []byte("a,b\n1,2\n"), 0o600)
	c := Open(filepath.Join(dir, "voices.csv"))
	if _, err := c.Import(src); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "voices.csv")); !os.IsNotExist(err) {
		t.Fatal("a bad import replaced the catalog")
	}
}

func TestSearchAll(t *testing.T) {
	c := catalog(t)
	got, total, err := c.Search(Query{All: true, Limit: 1})
	if err != nil || len(got) != 4 || total != 4 {
		t.Fatalf("%d of %d, %v", len(got), total, err)
	}
}

func TestAccents(t *testing.T) {
	got, err := catalog(t).Accents()
	if err != nil {
		t.Fatal(err)
	}
	want := []AccentCount{{Accent: "American", Total: 3, Female: 1, Male: 1, Neutral: 1}, {Accent: "Vietnamese", Total: 1, Female: 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v", got)
	}
}
