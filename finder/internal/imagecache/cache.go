// Package imagecache は美術館の画像を取得してリサイズ・WebP 化し、
// オブジェクトストレージ (R2) に無期限で置く。
//
// 館への取得は館ごとに 10 秒に 1 回へ絞る。設計の根拠は spec_image_cache.md。
package imagecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultWidths は生成するサイズ。1600 が詳細表示、400 が一覧のサムネ。
// 400 は 1600 の WebP からではなく必ず原本から作る (spec_image_cache.md §6)。
var DefaultWidths = []int{400, 1600}

type Options struct {
	DBPath      string // cache.db
	Store       Blob
	Interval    time.Duration // 館ごとの取得間隔 (既定 10s)
	Widths      []int
	Quality     int
	HTTPTimeout time.Duration
	UserAgent   string
	Converter   Converter
	Logger      *log.Logger
}

type Cache struct {
	db     *sql.DB
	store  Blob
	conv   Converter
	widths []int
	iv     time.Duration
	hc     *http.Client
	ua     string
	log    *log.Logger

	mu       sync.Mutex
	lastRun  map[string]time.Time // 館ごとの最終取得時刻
	inFlight map[string]bool
	wg       sync.WaitGroup
}

func New(ctx context.Context, o Options) (*Cache, error) {
	if o.Store == nil {
		return nil, errors.New("保管先が設定されていません")
	}
	if o.Interval <= 0 {
		o.Interval = 10 * time.Second
	}
	if len(o.Widths) == 0 {
		o.Widths = DefaultWidths
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 3 * time.Minute
	}
	if o.UserAgent == "" {
		o.UserAgent = "uchibi-finder/1.0 (+https://github.com/typewriter/home-museum)"
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	if o.Quality > 0 {
		o.Converter.Quality = o.Quality
	}
	if err := o.Converter.Check(ctx); err != nil {
		return nil, err
	}
	db, err := openCacheDB(o.DBPath)
	if err != nil {
		return nil, err
	}
	c := &Cache{
		db: db, store: o.Store, conv: o.Converter, widths: o.Widths,
		iv: o.Interval, ua: o.UserAgent, log: o.Logger,
		hc:       &http.Client{Timeout: o.HTTPTimeout},
		lastRun:  map[string]time.Time{},
		inFlight: map[string]bool{},
	}
	if n, err := c.recoverStuck(ctx); err != nil {
		db.Close()
		return nil, err
	} else if n > 0 {
		c.log.Printf("画像キャッシュ: 中断された %d 件をキューに戻しました", n)
	}
	return c, nil
}

func (c *Cache) Describe() string {
	return fmt.Sprintf("%s / 館ごと %s に 1 回 / 幅 %v", c.store.Describe(), c.iv, c.widths)
}

func (c *Cache) Close() error {
	c.wg.Wait()
	return c.db.Close()
}

// Status は 1 作品の状態。web 層がそのまま JSON にして返す。
type Status struct {
	State    string `json:"state"`
	Ready    bool   `json:"ready"`
	Ahead    int    `json:"ahead"`             // 同じ館のキューで自分より前にいる枚数
	ETASec   int    `json:"eta_sec,omitempty"` // 上を取得間隔で割った目安
	Bytes    int64  `json:"bytes,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Error    string `json:"error,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
}

// Request は 1 作品の状態を返し、未登録なら取得キューに積む。
// gone / failed を勝手に queued へ戻すことはしない。
func (c *Cache) Request(ctx context.Context, r Ref) (Status, error) {
	if r.ImageURL == "" {
		return Status{State: StateGone, Error: "images.image_url が空です"}, nil
	}
	origin := OriginURL(r, maxInt(c.widths))
	if err := c.enqueue(ctx, r, origin); err != nil {
		return Status{}, err
	}
	e, ok, err := c.lookup(ctx, Hash(r.SourceURL))
	if err != nil || !ok {
		return Status{}, err
	}
	return c.status(ctx, e)
}

// Lookup は積まずに状態だけ見る。一覧のように大量に問い合わせる場面で使う。
func (c *Cache) Lookup(ctx context.Context, sourceURL string) (Status, bool, error) {
	e, ok, err := c.lookup(ctx, Hash(sourceURL))
	if err != nil || !ok {
		return Status{}, false, err
	}
	st, err := c.status(ctx, e)
	return st, true, err
}

func (c *Cache) status(ctx context.Context, e Entry) (Status, error) {
	st := Status{
		State: e.State, Ready: e.State == StateReady, Bytes: e.Bytes,
		Width: e.PixelW, Height: e.PixelH, Error: e.LastError, Attempts: e.Attempts,
	}
	if e.State == StateQueued || e.State == StateFetching {
		n, err := c.ahead(ctx, e)
		if err != nil {
			return st, err
		}
		st.Ahead = n
		st.ETASec = int((time.Duration(n) * c.iv).Seconds())
	}
	return st, nil
}

// Open は保存済みの画像を読む。無ければ ErrNotFound。
func (c *Cache) Open(ctx context.Context, r Ref, width int) (io.ReadCloser, int64, error) {
	h := Hash(r.SourceURL)
	e, ok, err := c.lookup(ctx, h)
	if err != nil {
		return nil, 0, err
	}
	if !ok || !e.HasVariant(width) {
		return nil, 0, ErrNotFound
	}
	return c.store.Get(ctx, Key(e.Source, h, width))
}

// Run はワーカーを回す。ctx が切れるまで戻らない。
//
// 館ごとに 1 本ずつ goroutine を立てるのではなく、1 秒ごとに「仕事があって、
// かつ前回から間隔が空いた館」を探して起動する。館の集合が動的に決まるので
// このほうが素直。
func (c *Cache) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			c.wg.Wait()
			return
		case now := <-tick.C:
			sources, err := c.pendingSources(ctx, now)
			if err != nil {
				if ctx.Err() == nil {
					c.log.Printf("画像キャッシュ: キューを読めません: %v", err)
				}
				continue
			}
			for _, s := range sources {
				if !c.reserve(s, now) {
					continue
				}
				c.wg.Add(1)
				go func(src string) {
					defer c.wg.Done()
					defer c.release(src)
					c.step(ctx, src)
				}(s)
			}
		}
	}
}

// reserve は「この館をいま叩いてよいか」を判定して枠を取る。
// レート制限が館ごとである根拠は spec_image_cache.md §3。
func (c *Cache) reserve(source string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inFlight[source] {
		return false
	}
	if last, ok := c.lastRun[source]; ok && now.Sub(last) < c.iv {
		return false
	}
	c.inFlight[source] = true
	c.lastRun[source] = now
	return true
}

func (c *Cache) release(source string) {
	c.mu.Lock()
	c.inFlight[source] = false
	c.mu.Unlock()
}

// step は 1 館ぶん 1 枚を処理する。
func (c *Cache) step(ctx context.Context, source string) {
	e, ok, err := c.claim(ctx, source, time.Now())
	if err != nil {
		if ctx.Err() == nil {
			c.log.Printf("画像キャッシュ[%s]: 取り出しに失敗: %v", source, err)
		}
		return
	}
	if !ok {
		return
	}
	start := time.Now()
	if err := c.fetchOne(ctx, e); err != nil {
		var perm *permanentError
		if errors.As(err, &perm) {
			c.markGone(context.WithoutCancel(ctx), e.Hash, err)
			c.log.Printf("画像キャッシュ[%s]: id=%d 恒久失敗: %v", source, e.ImageID, err)
			return
		}
		c.markFailed(context.WithoutCancel(ctx), e, err)
		c.log.Printf("画像キャッシュ[%s]: id=%d 失敗 (%d 回目): %v",
			source, e.ImageID, e.Attempts+1, err)
		return
	}
	c.log.Printf("画像キャッシュ[%s]: id=%d 完了 (%s)",
		source, e.ImageID, time.Since(start).Round(time.Millisecond))
}

type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

func permanent(format string, a ...any) error {
	return &permanentError{fmt.Errorf(format, a...)}
}

// fetchOne は 1 枚を 取得 → 変換 → 保管 する。
func (c *Cache) fetchOne(ctx context.Context, e Entry) error {
	dir, err := os.MkdirTemp("", "finder-img-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "origin")
	if err := c.download(ctx, e, src); err != nil {
		return err
	}
	vs, err := c.conv.Convert(ctx, src, dir, c.widths)
	if err != nil {
		// 変換に失敗するものは中身が画像でない可能性が高い。
		return permanent("変換できません: %w", err)
	}
	for _, v := range vs {
		f, err := os.Open(v.Path)
		if err != nil {
			return err
		}
		err = c.store.Put(ctx, Key(e.Source, e.Hash, v.Width), f, v.Bytes, "image/webp")
		f.Close()
		if err != nil {
			return fmt.Errorf("保管に失敗 (%dpx): %w", v.Width, err)
		}
	}
	return c.markReady(context.WithoutCancel(ctx), e.Hash, vs)
}

func (c *Cache) download(ctx context.Context, e Entry, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.OriginURL, nil)
	if err != nil {
		return permanent("URL が不正です: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	for k, v := range requestHeaders(e.Source) {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("取得に失敗: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return permanent("館が %d を返しました", resp.StatusCode)
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusUnauthorized:
		// Cloudflare の Managed Challenge (cf-mitigated: challenge) は
		// ヘッダーで回避できる問題ではなく (実測: UA/Referer/cf_clearance
		// いずれを足しても通らない)、リトライしても無駄なので恒久扱いにする。
		// AIC は 2025-12 頃に www.artic.edu 全体へこれを導入し、以降ずっと
		// このまま (github art-institute-of-chicago/data-aggregator#151)。
		// それ以外の 403 は UA / Referer の問題であることが多いので、恒久扱い
		// にすると origin.go を直しても復帰できなくなる。一時失敗のままにする。
		if resp.Header.Get("Cf-Mitigated") != "" {
			return permanent("Cloudflare のチャレンジで拒否されました (cf-mitigated: %s)", resp.Header.Get("Cf-Mitigated"))
		}
		return fmt.Errorf("館が %d を返しました (UA / Referer を確認)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("館が %d を返しました", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !looksLikeImage(ct) {
		return permanent("画像ではありません (Content-Type: %s)", ct)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("保存に失敗: %w", err)
	}
	if n == 0 {
		return permanent("本文が空です")
	}
	return nil
}

// Stats は運用状況。カバレッジ画面に出す。
type Stats struct {
	ByState  map[string]int64
	BySource []SourceStat
	Total    int64
	Bytes    int64
	Store    string
	Interval time.Duration
}

type SourceStat struct {
	Source  string
	Queued  int64
	Ready   int64
	Failed  int64
	Gone    int64
	Bytes   int64
	ETAText string
}

func (c *Cache) Stats(ctx context.Context) (*Stats, error) {
	s := &Stats{ByState: map[string]int64{}, Store: c.store.Describe(), Interval: c.iv}
	rows, err := c.db.QueryContext(ctx, `
		SELECT source,
		       sum(CASE WHEN state IN ('queued','fetching') THEN 1 ELSE 0 END),
		       sum(CASE WHEN state = 'ready'  THEN 1 ELSE 0 END),
		       sum(CASE WHEN state = 'failed' THEN 1 ELSE 0 END),
		       sum(CASE WHEN state = 'gone'   THEN 1 ELSE 0 END),
		       sum(bytes), count(*)
		  FROM image_cache GROUP BY source ORDER BY 7 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st SourceStat
		var total int64
		if err := rows.Scan(&st.Source, &st.Queued, &st.Ready, &st.Failed,
			&st.Gone, &st.Bytes, &total); err != nil {
			return nil, err
		}
		st.ETAText = humanDuration(time.Duration(st.Queued) * c.iv)
		s.ByState["queued"] += st.Queued
		s.ByState["ready"] += st.Ready
		s.ByState["failed"] += st.Failed
		s.ByState["gone"] += st.Gone
		s.Total += total
		s.Bytes += st.Bytes
		s.BySource = append(s.BySource, st)
	}
	return s, rows.Err()
}

func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f 時間", d.Hours())
	default:
		return fmt.Sprintf("%.1f 日", d.Hours()/24)
	}
}

func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}
