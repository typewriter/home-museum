// author_merger は author_merge_batch.rb と Gemini API を繋ぐバッチ実行ツール。
//
//	cd importer/with_llms/author_merger
//	GEMINI_API_KEY=xxx go run . -batches 10 -size 20
//
// 1バッチの流れは author_migration_prompt.md の手順と同じ:
//
//	ruby author_merge_batch.rb next N   → .author_merge_batch.json
//	Gemini API で判定                   → decisions-<unixtime>.json
//	ruby author_merge_batch.rb append F → author_merges.csv へ追記し hm.db へ適用
//
// 判定の正本 (author_merges.csv) の管理と検証は Ruby 側が唯一の実装なので、
// ここでは触らない。途中で止めても再実行すれば続きから進む。
//
// 名寄せは「見逃しより誤統合のほうがコストが高い」作業なので、モデルが候補外の
// person_key を返した場合は採用せず訊き直す (Ruby 側でも弾かれるが、その時点では
// バッチ全体が中断してしまうため手前で落とす)。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "embed"

	"google.golang.org/genai"
)

//go:embed prompt.md
var systemInstruction string

// Ruby 側 (author_merge_batch.rb の検証) と揃える。none は「名寄せ先なし」。
var confidences = []string{"high", "medium", "low", "none"}

var (
	remainingRe = regexp.MustCompile(`remaining=(\d+)`)
	appendedRe  = regexp.MustCompile(`appended=(\d+)`)
	mergedRe    = regexp.MustCompile(`統合=(\d+)`)
)

// author_merge_batch.rb の next が書き出す形。
type artist struct {
	PersonKey    string   `json:"person_key"`
	DisplayName  string   `json:"display_name"`
	BirthYear    *int     `json:"birth_year"`
	DeathYear    *int     `json:"death_year"`
	ImageCount   int      `json:"image_count"`
	Sources      []string `json:"sources"`
	NameVariants []string `json:"name_variants"`
	Merged       bool     `json:"merged,omitempty"`
}

type item struct {
	artist
	Candidates []artist `json:"candidates"`
}

// モデルに返させる形。null を扱わせると取りこぼしが増えるので、
// 「名寄せ先なし」は空文字と "none" で表現させ、こちら側で null に直す。
type reply struct {
	PersonKey  string `json:"person_key"`
	MergeInto  string `json:"merge_into"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
}

// Ruby 側に渡す形。
type decision struct {
	PersonKey  string  `json:"person_key"`
	MergeInto  *string `json:"merge_into"`
	Confidence *string `json:"confidence"`
	Reason     string  `json:"reason"`
}

type options struct {
	importerDir string
	outDir      string
	rubyBin     string
	model       string
	size        int
	batches     int
	attempts    int
	temperature float64
	thinking    string
	tier        string
	timeout     time.Duration
	dryRun      bool

	thinkingCfg *genai.ThinkingConfig
	serviceTier genai.ServiceTier
}

func main() {
	log.SetFlags(log.Ltime)

	var opt options
	flag.StringVar(&opt.importerDir, "importer", "..", "author_merge_batch.rb のあるディレクトリ")
	flag.StringVar(&opt.outDir, "out", "", "判定結果JSONの出力先 (既定: <importer>/scratch/author_merges)")
	flag.StringVar(&opt.rubyBin, "ruby", "ruby", "ruby コマンド")
	flag.StringVar(&opt.model, "model", envOr("GEMINI_MODEL", "gemini-3.6-flash"), "Gemini のモデル名")
	flag.IntVar(&opt.size, "size", 20, "1バッチの件数")
	flag.IntVar(&opt.batches, "batches", 10, "実行するバッチ数 (0 なら remaining=0 まで)")
	flag.IntVar(&opt.attempts, "attempts", 3, "1バッチあたりのAPI試行回数の上限")
	flag.Float64Var(&opt.temperature, "temperature", 1.0, "生成温度 (思考モデルは既定の 1.0 のままが推奨)")
	flag.StringVar(&opt.thinking, "thinking", "high", "思考レベル (minimal|low|medium|high|default)")
	flag.StringVar(&opt.tier, "tier", "flex", "サービスティア (flex|standard|default)。flex はレイテンシが高い代わりに安い")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Minute, "1リクエストのタイムアウト (flex は最大15分待たされる。0 で無効)")
	flag.BoolVar(&opt.dryRun, "dry-run", false, "判定結果JSONを書くところまでで止め、CSV/DBへ反映しない")
	flag.Parse()

	if err := run(context.Background(), opt); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(ctx context.Context, opt options) error {
	dir, err := filepath.Abs(opt.importerDir)
	if err != nil {
		return err
	}
	opt.importerDir = dir
	if opt.thinkingCfg, err = thinkingConfig(opt.thinking); err != nil {
		return err
	}
	if opt.serviceTier, err = serviceTier(opt.tier); err != nil {
		return err
	}
	if opt.outDir == "" {
		opt.outDir = filepath.Join(dir, "scratch", "author_merges")
	}
	if err := os.MkdirAll(opt.outDir, 0o755); err != nil {
		return err
	}

	apiKey := envOr("GEMINI_API_KEY", os.Getenv("GOOGLE_API_KEY"))
	if apiKey == "" {
		return errors.New("GEMINI_API_KEY (または GOOGLE_API_KEY) が未設定")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey, Backend: genai.BackendGeminiAPI})
	if err != nil {
		return fmt.Errorf("genai クライアントの生成に失敗: %w", err)
	}

	log.Printf("model=%s size=%d (author_merges.csv へ追記し hm.db へ適用)", opt.model, opt.size)

	appended, merged := 0, 0
	for i := 1; opt.batches == 0 || i <= opt.batches; i++ {
		batch, remaining, err := nextBatch(opt)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			log.Printf("remaining=0 のため終了 (完了 %d バッチ / 判定 %d 件 / うち統合 %d 件)", i-1, appended, merged)
			return nil
		}
		log.Printf("[%d] batch=%d件 remaining=%d", i, len(batch), remaining)

		decisions, err := judgeBatch(ctx, client, opt, batch)
		if err != nil {
			return fmt.Errorf("バッチ %d の判定に失敗: %w", i, err)
		}

		path := filepath.Join(opt.outDir, fmt.Sprintf("decisions-%d.json", time.Now().UnixNano()))
		if err := writeJSON(path, decisions); err != nil {
			return err
		}
		if opt.dryRun {
			log.Printf("[%d] dry-run: %s に書き出して終了", i, path)
			return nil
		}

		n, m, remaining, err := appendBatch(opt, path)
		if err != nil {
			return fmt.Errorf("バッチ %d の追記に失敗 (%s): %w", i, path, err)
		}
		appended += n
		merged += m
		log.Printf("[%d] appended=%d件 (統合 %d) remaining=%d", i, n, m, remaining)
	}

	log.Printf("完了: %d バッチ / 判定 %d 件 / うち統合 %d 件", opt.batches, appended, merged)
	return nil
}

func nextBatch(opt options) ([]item, int, error) {
	out, err := runRuby(opt, "next", strconv.Itoa(opt.size))
	if err != nil {
		return nil, 0, err
	}
	remaining := firstInt(remainingRe, out)

	raw, err := os.ReadFile(filepath.Join(opt.importerDir, ".author_merge_batch.json"))
	if err != nil {
		return nil, remaining, err
	}
	var batch []item
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, remaining, fmt.Errorf("バッチJSONの解析に失敗: %w", err)
	}
	return batch, remaining, nil
}

func appendBatch(opt options, path string) (appended, merged, remaining int, err error) {
	out, err := runRuby(opt, "append", path)
	if err != nil {
		return 0, 0, 0, err
	}
	return firstInt(appendedRe, out), firstInt(mergedRe, out), firstInt(remainingRe, out), nil
}

func runRuby(opt options, args ...string) (string, error) {
	args = append([]string{"author_merge_batch.rb"}, args...)
	cmd := exec.Command(opt.rubyBin, args...)
	cmd.Dir = opt.importerDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", opt.rubyBin, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// judgeBatch は 1 バッチを判定する。取りこぼした person_key は残りだけを
// 入力にして訊き直す (全件揃うか attempts 回に達するまで)。
func judgeBatch(ctx context.Context, client *genai.Client, opt options, batch []item) ([]decision, error) {
	got := make(map[string]decision, len(batch))
	pending := batch

	var lastErr error
	for attempt := 1; attempt <= opt.attempts && len(pending) > 0; attempt++ {
		if attempt > 1 {
			wait := time.Duration(attempt-1) * 5 * time.Second
			log.Printf("  再試行 %d/%d: 未取得 %d件 (%v 待機)", attempt, opt.attempts, len(pending), wait)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		replies, err := generate(ctx, client, opt, pending)
		if err != nil {
			lastErr = err
			log.Printf("  API エラー: %v", err)
			continue
		}

		want := make(map[string]item, len(pending))
		for _, it := range pending {
			want[it.PersonKey] = it
		}
		for _, r := range replies {
			it, ok := want[r.PersonKey]
			if !ok {
				continue // 入力に無い person_key は捨てる
			}
			d, err := validate(r, it)
			if err != nil {
				log.Printf("  不採用 %s: %v", r.PersonKey, err)
				continue
			}
			got[r.PersonKey] = d
		}

		var rest []item
		for _, it := range pending {
			if _, ok := got[it.PersonKey]; !ok {
				rest = append(rest, it)
			}
		}
		pending = rest
	}

	if len(pending) > 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("%d件の判定を取得できなかった: %w", len(pending), lastErr)
		}
		return nil, fmt.Errorf("%d件の判定を取得できなかった (先頭: %s)", len(pending), pending[0].PersonKey)
	}

	out := make([]decision, 0, len(batch))
	for _, it := range batch {
		out = append(out, got[it.PersonKey])
	}
	return out, nil
}

// validate はモデルの回答を検証して Ruby 側に渡す形へ直す。
// 統合先は「その要素の候補に含まれるもの」だけを認める。誤統合のコストが高い作業
// なので、候補外のキーを返してきたら採用せず訊き直させる。
func validate(r reply, it item) (decision, error) {
	reason := strings.TrimSpace(r.Reason)
	if reason == "" {
		return decision{}, errors.New("reason が空")
	}

	into := strings.TrimSpace(r.MergeInto)
	if into == "" || strings.EqualFold(into, "null") || strings.EqualFold(into, "none") {
		return decision{PersonKey: r.PersonKey, Reason: reason}, nil
	}
	if into == it.PersonKey {
		return decision{}, errors.New("自分自身への統合")
	}

	found := false
	for _, c := range it.Candidates {
		if c.PersonKey == into {
			found = true
			break
		}
	}
	if !found {
		return decision{}, fmt.Errorf("候補に無い統合先: %s", into)
	}
	if !contains(confidences[:3], r.Confidence) {
		return decision{}, fmt.Errorf("不正な confidence: %q", r.Confidence)
	}

	conf := r.Confidence
	return decision{PersonKey: r.PersonKey, MergeInto: &into, Confidence: &conf, Reason: reason}, nil
}

func generate(ctx context.Context, client *genai.Client, opt options, batch []item) ([]reply, error) {
	input, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return nil, err
	}

	temp := float32(opt.temperature)
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: systemInstruction}}},
		Temperature:       &temp,
		ThinkingConfig:    opt.thinkingCfg,
		ServiceTier:       opt.serviceTier,
		ResponseMIMEType:  "application/json",
		ResponseSchema: &genai.Schema{
			Type: genai.TypeArray,
			Items: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"person_key": {Type: genai.TypeString},
					"merge_into": {Type: genai.TypeString, Description: "候補の person_key。名寄せ先が無ければ空文字"},
					"confidence": {Type: genai.TypeString, Enum: confidences},
					"reason":     {Type: genai.TypeString},
				},
				PropertyOrdering: []string{"person_key", "merge_into", "confidence", "reason"},
				Required:         []string{"person_key", "merge_into", "confidence", "reason"},
			},
		},
	}

	if opt.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.timeout)
		defer cancel()
	}

	prompt := fmt.Sprintf("次の %d 件について名寄せ先を判定してください。\n\n%s", len(batch), input)
	resp, err := client.Models.GenerateContent(ctx, opt.model, genai.Text(prompt), config)
	if err != nil {
		return nil, err
	}

	text := resp.Text()
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("空のレスポンス")
	}
	var replies []reply
	if err := json.Unmarshal([]byte(text), &replies); err != nil {
		return nil, fmt.Errorf("レスポンスJSONの解析に失敗: %w", err)
	}
	return replies, nil
}

func thinkingConfig(level string) (*genai.ThinkingConfig, error) {
	var l genai.ThinkingLevel
	switch strings.ToLower(level) {
	case "", "default":
		return nil, nil
	case "minimal":
		l = genai.ThinkingLevelMinimal
	case "low":
		l = genai.ThinkingLevelLow
	case "medium":
		l = genai.ThinkingLevelMedium
	case "high":
		l = genai.ThinkingLevelHigh
	default:
		return nil, fmt.Errorf("不正な -thinking: %s (minimal|low|medium|high|default)", level)
	}
	return &genai.ThinkingConfig{ThinkingLevel: l}, nil
}

func serviceTier(tier string) (genai.ServiceTier, error) {
	switch strings.ToLower(tier) {
	case "", "default":
		return "", nil
	case "flex":
		return genai.ServiceTierFlex, nil
	case "standard":
		return genai.ServiceTierStandard, nil
	default:
		return "", fmt.Errorf("不正な -tier: %s (flex|standard|default)", tier)
	}
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func firstInt(re *regexp.Regexp, s string) int {
	if m := re.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
