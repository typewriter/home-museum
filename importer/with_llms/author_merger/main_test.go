package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// author_merge_batch.rb が書き出す JSON をこちらの構造体で読めるか。
// フィールド名がずれると実行時まで気づけないので、実物があるときは突き合わせる。
func TestBatchJSONContract(t *testing.T) {
	path := filepath.Join("..", ".author_merge_batch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s が無いので省略 (ruby author_merge_batch.rb next N で作れる)", path)
	}

	var batch []item
	if err := json.Unmarshal(raw, &batch); err != nil {
		t.Fatalf("解析できない: %v", err)
	}
	if len(batch) == 0 {
		t.Skip("バッチが空")
	}
	for i, it := range batch {
		if it.PersonKey == "" {
			t.Errorf("[%d] person_key が空", i)
		}
		if it.DisplayName == "" {
			t.Errorf("[%d] display_name が空", i)
		}
		if it.ImageCount == 0 {
			t.Errorf("[%d] image_count が 0", i)
		}
		if len(it.Sources) == 0 {
			t.Errorf("[%d] sources が空", i)
		}
		for j, c := range it.Candidates {
			if c.PersonKey == "" || c.DisplayName == "" {
				t.Errorf("[%d] 候補[%d] が空: %+v", i, j, c)
			}
		}
	}
}

func TestValidate(t *testing.T) {
	it := item{
		artist:     artist{PersonKey: "cluster:aic|1", DisplayName: "Jan Sadeler"},
		Candidates: []artist{{PersonKey: "ulan:500020677"}, {PersonKey: "cluster:met|2"}},
	}

	tests := []struct {
		name    string
		in      reply
		wantErr bool
		merged  bool
	}{
		{"候補への統合", reply{"cluster:aic|1", "ulan:500020677", "high", "同一人物"}, false, true},
		{"名寄せ先なし(空文字)", reply{"cluster:aic|1", "", "none", "候補に該当なし"}, false, false},
		{"名寄せ先なし(null文字列)", reply{"cluster:aic|1", "null", "none", "候補に該当なし"}, false, false},
		{"候補に無いキー", reply{"cluster:aic|1", "ulan:999", "high", "同一人物"}, true, false},
		{"自分自身", reply{"cluster:aic|1", "cluster:aic|1", "high", "同一人物"}, true, false},
		{"confidence が none のまま統合", reply{"cluster:aic|1", "ulan:500020677", "none", "同一人物"}, true, false},
		{"confidence が不正", reply{"cluster:aic|1", "ulan:500020677", "maybe", "同一人物"}, true, false},
		{"reason が空", reply{"cluster:aic|1", "ulan:500020677", "high", "   "}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validate(tt.in, it)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if (got.MergeInto != nil) != tt.merged {
				t.Fatalf("MergeInto=%v, merged=%v", got.MergeInto, tt.merged)
			}
			// 名寄せ先が無いときは confidence も null にして Ruby 側に渡す
			if got.MergeInto == nil && got.Confidence != nil {
				t.Fatalf("名寄せ先なしなのに confidence=%v", *got.Confidence)
			}
		})
	}
}

// Ruby 側が期待する形 (merge_into / confidence が null) で書き出せているか。
func TestDecisionJSONShape(t *testing.T) {
	into, conf := "ulan:1", "high"
	data, err := json.Marshal([]decision{
		{PersonKey: "a", MergeInto: &into, Confidence: &conf, Reason: "r"},
		{PersonKey: "b", Reason: "候補に該当なし"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"person_key":"a","merge_into":"ulan:1","confidence":"high","reason":"r"},` +
		`{"person_key":"b","merge_into":null,"confidence":null,"reason":"候補に該当なし"}]`
	if string(data) != want {
		t.Fatalf("got  %s\nwant %s", data, want)
	}
}
