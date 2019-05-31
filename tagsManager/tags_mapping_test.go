package tagsManager

import (
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
