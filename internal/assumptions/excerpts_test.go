package assumptions

import (
	"strings"
	"testing"
)

func TestDifferenceExcerptsPreserveNumbersMissingAndEscapedPaths(t *testing.T) {
	left := `{"a/b":{"~key":9007199254740993},"missing":null}`
	right := `{"a/b":{"~key":9007199254740992}}`
	got := differenceExcerpts(left, right, differencePaths(left, right))
	if len(got) != 2 || got[0].Path != "response/a~1b/~0key" || got[0].Expected != "9007199254740993" || got[0].Actual != "9007199254740992" || got[1].Expected != "null" || got[1].Actual != "<missing>" {
		t.Fatalf("lost difference: %+v", got)
	}
	if value := excerptAt(strings.Repeat("é", 400), nil); len([]rune(value)) != 257 {
		t.Fatalf("unbounded excerpt: %d", len([]rune(value)))
	}
}
