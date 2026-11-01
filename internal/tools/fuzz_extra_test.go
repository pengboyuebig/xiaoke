package tools

import (
	"strings"
	"testing"
)

// TestUnifiedDiffEdgeCases：空串/纯空白/超长行/非法 UTF-8 等边界不得 panic。
func TestUnifiedDiffEdgeCases(t *testing.T) {
	cases := [][2]string{
		{"", ""},
		{"", "a"},
		{"a", ""},
		{"\n", ""},
		{"", "\n\n\n"},
		{"a\n", "a"},
		{"a", "a\n"},
		{"   ", "\t\t"},
		{"\x00\x01", "\x00\x02"},
		{strings.Repeat("x", 5000), strings.Repeat("y", 5000)},
		{"中文一\n中文二", "中文三\n中文四"},
		{"\r\n\r\n", "\n\n"},
	}
	for i, c := range cases {
		d := UnifiedDiff(c[0], c[1], 3)
		if c[0] == c[1] && d != "" {
			t.Errorf("case %d: 相同内容应无 diff", i)
		}
		_ = d // 不 panic 即可
	}
}

// TestUnifiedDiffIdenticalAfterNormalize：仅 CRLF 差异不应产生内容变更行。
func TestUnifiedDiffCRLFOnly(t *testing.T) {
	d := UnifiedDiff("a\r\nb\r\nc", "a\nb\nc", 3)
	if strings.Contains(d, "-a") || strings.Contains(d, "+a") {
		t.Fatalf("仅行尾差异不应标记内容变更: %q", d)
	}
}
