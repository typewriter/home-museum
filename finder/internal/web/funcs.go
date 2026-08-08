package web

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"
)

var funcs = template.FuncMap{
	"comma":    comma,
	"pct":      func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "%" },
	"trunc":    trunc,
	"urlWith":  urlWith,
	"has":      has,
	"dur":      dur,
	"mib":      func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) },
	"add":      func(a, b int) int { return a + b },
	"dict":     dict,
	"orElse":   orElse,
	"argPairs": argPairs,
}

// comma は 1361997 を "1,361,997" にする。件数が桁で読めないと点検に使えない。
// テンプレートからは int / int64 の両方が来るので any で受ける。
func comma(v any) string {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int32:
		n = int64(x)
	case int64:
		n = x
	case float64:
		n = int64(x)
	default:
		return fmt.Sprint(v)
	}
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func trunc(n int, s string) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func dur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.2f s", d.Seconds())
	}
}

func dict(pairs ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if k, ok := pairs[i].(string); ok {
			m[k] = pairs[i+1]
		}
	}
	return m
}

// orElse は空文字を既定値で置き換える。hm.db は NULL と ” が混在するので、
// 一覧では両方まとめて「—」にしたい場面が多い。
func orElse(def, v string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// argPairs はバインド変数を画面に出すための整形。SQL だけ見せても、何を
// 渡したか分からないと点検にならない。
func argPairs(args []any) string {
	if len(args) == 0 {
		return "(なし)"
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprintf("%d: %#v", i+1, a)
	}
	return strings.Join(parts, "  ")
}
