package update

import "testing"

func TestParseSemver(t *testing.T) {
	good := []string{"0.1.0", "v0.1.0", "V1.2.3", "0.1.0-rc.6", "1.2.3-alpha.1+build.5", "v0.1.0-rc.6"}
	for _, s := range good {
		if _, ok := parseSemver(s); !ok {
			t.Errorf("parseSemver(%q) 应成功", s)
		}
	}
	bad := []string{"", "dev", "0.1", "0.1.0.0", "a.b.c", "0.x.1", "v", "0.1.0-"}
	for _, s := range bad {
		if _, ok := parseSemver(s); ok {
			t.Errorf("parseSemver(%q) 应失败", s)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"v0.1.0", "0.1.0", 0},
		{"0.2.0", "0.1.9", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.1.0", "0.1.0-rc.6", 1},   // 正式版 > 预发布
		{"0.1.0-rc.6", "0.1.0", -1},  // 预发布 < 正式版
		{"0.1.0-rc.7", "0.1.0-rc.6", 1},
		{"0.1.0-rc.10", "0.1.0-rc.9", 1},  // 数值比较
		{"0.1.0-rc.9", "0.1.0-rc.10", -1}, // 数值比较
		{"0.1.0-alpha", "0.1.0-alpha.1", -1},
		{"0.1.0-rc.1", "0.1.0-beta", 1}, // 字母按 ASCII：beta < rc
	}
	for _, c := range cases {
		a, ok1 := parseSemver(c.a)
		b, ok2 := parseSemver(c.b)
		if !ok1 || !ok2 {
			t.Errorf("用例 %q / %q 解析失败", c.a, c.b)
			continue
		}
		if got := compareSemver(a, b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}
