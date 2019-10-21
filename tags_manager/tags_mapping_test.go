package tags_manager

import (
	"fmt"
	"testing"
)

func TestSanitizeTags(t *testing.T) {
	got, err := SanitizeTags([]string{"this", "super", "expensive", "test", "has", "a lot of", "crypto", "currency", "in it", "trump", "will build the", "wall"}, "UCNQfQvFMPnInwsU_iGYArJQ")
	if err != nil {
		t.Error(err)
		return
	}
	expectedTags := []string{
		"blockchain",
		"switzerland",
		"news",
		"science & technology",
		"economics",
		"experiments",
		"this",
		"in it",
		"will build the",
		"has",
		"crypto",
		"trump",
		"wall",
		"expensive",
		"currency",
		"a lot of",
	}
	if len(expectedTags) != len(got) {
		t.Error("number of tags differ")
		return
	}
outer:
	for _, et := range expectedTags {
		for _, t := range got {
			if et == t {
				continue outer
			}
		}
		t.Error("tag not found")
		return
	}

}
func TestNormalizeTag(t *testing.T) {
	tags := []string{
		"blockchain",
		"Switzerland",
		"news         ",
		"  science  &  Technology  ",
		"economics",
		"experiments",
		"this",
		"in it",
		"will build the (WOOPS)",
		"~has",
		"crypto",
		"trump",
		"wall",
		"expensive",
		"!currency",
		" a lot of  ",
		"#",
		"#whatever",
		"#123",
		"#123 Something else",
		"#123aaa",
		"!asdasd",
		"CASA BLANCA",
		"wwe 2k18 Elimination chamber!",
		"pero'",
		"però",
		"è proprio",
		"Ep 29",
		"sctest29 Keddr",
		"mortal kombat 11 shang tsung",
		"!asdasd!",
	}
	normalizedTags := make([]string, 0, len(tags))
	for _, tag := range tags {
		got, err := normalizeTag(tag)
		if err != nil {
			t.Error(err)
			return
		}
		if got != "" {
			normalizedTags = append(normalizedTags, got)
		}
		fmt.Printf("Got tag: '%s'\n", got)
	}
	expected := []string{
		"blockchain",
		"switzerland",
		"news",
		"science & technology",
		"economics",
		"experiments",
		"this",
		"in it",
		"will build the",
		"has",
		"crypto",
		"trump",
		"wall",
		"expensive",
		"currency",
		"a lot of",
		"whatever",
		"123",
		"something else",
		"123aaa",
		"asdasd",
		"casa blanca",
		"wwe 2k18 elimination chamber",
		"pero",
		"però",
		"è proprio",
		"ep 29",
		"sctest29 keddr",
		"mortal kombat 11 shang tsung",
		"asdasd",
	}
	if !Equal(normalizedTags, expected) {
		t.Error("result not as expected")
		return
	}

}
func Equal(a, b []string) bool {
	if len(a) != len(b) {
		fmt.Printf("expected length %d but got %d", len(b), len(a))
		return false
	}
	for i, v := range a {
		if v != b[i] {
			fmt.Printf("expected %s but bot %s\n", b[i], v)
			return false
		}
	}
	return true
}
