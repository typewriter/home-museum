// title_translator は title_translation_batch.rb と Gemini API を繋ぐバッチ実行ツール。
//
//	cd importer/with_llms/title_translator
//	GEMINI_API_KEY=xxx go run . -source cleveland -batches 10 -size 50
//
// 1バッチの流れは title_translation_prompt.md の手順と同じ (S = -source の値):
//
//	ruby title_translation_batch.rb S next N   → .title_translation_batch_S.json
//	Gemini API で翻訳                          → translations-<unixtime>.json
//	ruby title_translation_batch.rb S append F → titles_ja_S.csv へ追記
//
// LMDB の読み出しと CSV の検証は Ruby 側が唯一の実装なので、ここでは触らない。
// 進捗の状態も CSV そのものなので、途中で止めても再実行すれば続きから進む。
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

// Ruby 側 (TitleTranslationBatch::CONFIDENCES) と揃える。
var confidences = []string{"high", "medium", "low"}

// next / append が最後に出力する進捗行。
var progressRe = regexp.MustCompile(`total=(\d+) done=(\d+) remaining=(\d+)`)

// Ruby 側 (TitleTranslationBatch::FIELDS) と揃える。images の共通スキーマなので
// ソースが変わってもフィールド名は同じ。
type record struct {
	SourceURL string `json:"source_url"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Date      string `json:"date"`
	Category  string `json:"category"`
	Style     string `json:"style"`
	Medium    string `json:"medium"`
	Origin    string `json:"origin"`
}

type translation struct {
	SourceURL  string `json:"source_url"`
	TitleJa    string `json:"title_ja"`
	Confidence string `json:"confidence"`
}

type progress struct {
	total, done, remaining int
}

type options struct {
	source      string
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

	thinkingCfg *genai.ThinkingConfig // thinking をパースしたもの (run で設定)
	serviceTier genai.ServiceTier     // tier をパースしたもの (run で設定)
}

func main() {
	log.SetFlags(log.Ltime)

	var opt options
	flag.StringVar(&opt.source, "source", "", "対象ソース名 (aic|met|parismusees|rijksmuseum|smithsonian|cleveland)")
	flag.StringVar(&opt.importerDir, "importer", "..", "title_translation_batch.rb のあるディレクトリ")
	flag.StringVar(&opt.outDir, "out", "", "翻訳結果JSONの出力先 (既定: <importer>/scratch/translations)")
	flag.StringVar(&opt.rubyBin, "ruby", "ruby", "ruby コマンド")
	flag.StringVar(&opt.model, "model", envOr("GEMINI_MODEL", "gemini-3.6-flash"), "Gemini のモデル名")
	flag.IntVar(&opt.size, "size", 50, "1バッチの件数")
	flag.IntVar(&opt.batches, "batches", 10, "実行するバッチ数 (0 なら remaining=0 まで)")
	flag.IntVar(&opt.attempts, "attempts", 3, "1バッチあたりのAPI試行回数の上限")
	flag.Float64Var(&opt.temperature, "temperature", 1.0, "生成温度 (思考モデルは既定の 1.0 のままが推奨)")
	flag.StringVar(&opt.thinking, "thinking", "high", "思考レベル (minimal|low|medium|high|default)")
	flag.StringVar(&opt.tier, "tier", "flex", "サービスティア (flex|standard|default)。flex はレイテンシが高い代わりに安い")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Minute, "1リクエストのタイムアウト (flex は最大15分待たされる。0 で無効)")
	flag.BoolVar(&opt.dryRun, "dry-run", false, "翻訳結果JSONを書くところまでで止め、CSVへ追記しない")
	flag.Parse()

	if err := run(context.Background(), opt); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run(ctx context.Context, opt options) error {
	// 取り違えると別ソースの CSV に追記してしまうため既定値は設けない。
	// 名前そのものの妥当性は Ruby 側 (Loaders::SOURCES) が検証する。
	if opt.source == "" {
		return errors.New("-source が未指定 (ruby title_translation_batch.rb sources で一覧)")
	}

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
		opt.outDir = filepath.Join(dir, "scratch", "translations", opt.source)
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

	log.Printf("source=%s model=%s (titles_ja_%s.csv へ追記)", opt.source, opt.model, opt.source)

	appended := 0
	for i := 1; opt.batches == 0 || i <= opt.batches; i++ {
		batch, prog, err := nextBatch(opt)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			log.Printf("remaining=0 のため終了 (完了 %d バッチ / 追記 %d 件)", i-1, appended)
			return nil
		}
		log.Printf("[%d] batch=%d件 total=%d done=%d remaining=%d", i, len(batch), prog.total, prog.done, prog.remaining)

		translations, err := translateBatch(ctx, client, opt, batch)
		if err != nil {
			return fmt.Errorf("バッチ %d の翻訳に失敗: %w", i, err)
		}

		path := filepath.Join(opt.outDir, fmt.Sprintf("translations-%d.json", time.Now().UnixNano()))
		if err := writeJSON(path, translations); err != nil {
			return err
		}
		if opt.dryRun {
			log.Printf("[%d] dry-run: %s に書き出して終了", i, path)
			return nil
		}

		n, prog, err := appendBatch(opt, path)
		if err != nil {
			return fmt.Errorf("バッチ %d の追記に失敗 (%s): %w", i, path, err)
		}
		appended += n
		log.Printf("[%d] appended=%d件 done=%d remaining=%d", i, n, prog.done, prog.remaining)
	}

	log.Printf("完了: %d バッチ / 追記 %d 件", opt.batches, appended)
	return nil
}

// nextBatch は未翻訳の先頭 size 件を Ruby 側に書き出させ、それを読み込む。
func nextBatch(opt options) ([]record, progress, error) {
	out, err := runRuby(opt, "next", strconv.Itoa(opt.size))
	if err != nil {
		return nil, progress{}, err
	}
	prog := parseProgress(out)

	raw, err := os.ReadFile(filepath.Join(opt.importerDir, ".title_translation_batch_"+opt.source+".json"))
	if err != nil {
		return nil, prog, err
	}
	var batch []record
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, prog, fmt.Errorf("バッチJSONの解析に失敗: %w", err)
	}
	return batch, prog, nil
}

func appendBatch(opt options, path string) (int, progress, error) {
	out, err := runRuby(opt, "append", path)
	if err != nil {
		return 0, progress{}, err
	}
	n := 0
	if m := regexp.MustCompile(`appended=(\d+)`).FindStringSubmatch(out); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	return n, parseProgress(out), nil
}

func runRuby(opt options, args ...string) (string, error) {
	args = append([]string{"title_translation_batch.rb", opt.source}, args...)
	cmd := exec.Command(opt.rubyBin, args...)
	cmd.Dir = opt.importerDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", opt.rubyBin, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func parseProgress(out string) progress {
	m := progressRe.FindStringSubmatch(out)
	if m == nil {
		return progress{}
	}
	total, _ := strconv.Atoi(m[1])
	done, _ := strconv.Atoi(m[2])
	remaining, _ := strconv.Atoi(m[3])
	return progress{total: total, done: done, remaining: remaining}
}

// translateBatch は 1 バッチを翻訳する。モデルが取りこぼした source_url は
// 残りだけを入力にして訊き直す (全件揃うか attempts 回に達するまで)。
func translateBatch(ctx context.Context, client *genai.Client, opt options, batch []record) ([]translation, error) {
	got := make(map[string]translation, len(batch))
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

		results, err := generate(ctx, client, opt, pending)
		if err != nil {
			lastErr = err
			log.Printf("  API エラー: %v", err)
			continue
		}

		want := make(map[string]bool, len(pending))
		for _, r := range pending {
			want[r.SourceURL] = true
		}
		for _, t := range results {
			if !want[t.SourceURL] {
				continue // 入力に無い source_url は捨てる (Ruby 側でも弾かれる)
			}
			if strings.TrimSpace(t.TitleJa) == "" || !contains(confidences, t.Confidence) {
				continue
			}
			got[t.SourceURL] = t
		}

		var rest []record
		for _, r := range pending {
			if _, ok := got[r.SourceURL]; !ok {
				rest = append(rest, r)
			}
		}
		pending = rest
	}

	if len(pending) > 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("%d件の翻訳を取得できなかった: %w", len(pending), lastErr)
		}
		return nil, fmt.Errorf("%d件の翻訳を取得できなかった (先頭: %s)", len(pending), pending[0].SourceURL)
	}

	// 入力順を保って返す (Ruby 側は順序を問わないが、差分を読むときに楽)。
	out := make([]translation, 0, len(batch))
	for _, r := range batch {
		out = append(out, got[r.SourceURL])
	}
	return out, nil
}

func generate(ctx context.Context, client *genai.Client, opt options, batch []record) ([]translation, error) {
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
					"source_url": {Type: genai.TypeString},
					"title_ja":   {Type: genai.TypeString},
					"confidence": {Type: genai.TypeString, Enum: confidences},
				},
				PropertyOrdering: []string{"source_url", "title_ja", "confidence"},
				Required:         []string{"source_url", "title_ja", "confidence"},
			},
		},
	}

	if opt.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.timeout)
		defer cancel()
	}

	prompt := fmt.Sprintf("次の %d 件を翻訳してください。\n\n%s", len(batch), input)
	resp, err := client.Models.GenerateContent(ctx, opt.model, genai.Text(prompt), config)
	if err != nil {
		return nil, err
	}

	text := resp.Text()
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("空のレスポンス")
	}
	var results []translation
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		return nil, fmt.Errorf("レスポンスJSONの解析に失敗: %w", err)
	}
	return results, nil
}

// thinkingConfig は -thinking の値を ThinkingConfig に直す。
// "default" (と空文字) はモデル側の既定に任せるため nil を返す。
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

// serviceTier は -tier の値を ServiceTier に直す。
// "default" (と空文字) は指定なし = API 側の既定 (standard 相当)。
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
