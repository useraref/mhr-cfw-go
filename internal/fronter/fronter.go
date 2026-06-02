package fronter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/denuitt1/mhr-cfw/internal/codec"
	"github.com/denuitt1/mhr-cfw/internal/config"
	"github.com/denuitt1/mhr-cfw/internal/constants"
	"github.com/denuitt1/mhr-cfw/internal/h2"
	"github.com/denuitt1/mhr-cfw/internal/logging"
)

var log = logging.Get("Fronter")

// regex در سطح package کامپایل می‌شه — نه داخل هر تابع
var statusLineRegex = regexp.MustCompile(`\d{3}`)

type HostStat struct {
	Requests       int
	CacheHits      int
	Bytes          int
	TotalLatencyNs int64
	Errors         int
}

type DomainFronter struct {
	// لیست کامل IPs برای failover
	googleIPs    []string
	googleIPIdx  uint32
	connectHost  string
	sniHost      string
	sniHosts     []string
	sniIdx       uint32
	httpHost     string
	scriptIDs    []string
	scriptIdx    uint32
	devAvail     bool
	parallelRelay int

	perSite   map[string]*HostStat
	perSiteMu sync.RWMutex

	authKey   string
	verifySSL bool
	relayTO   time.Duration
	tlsTO     time.Duration
	maxResp   int

	h2 *h2.Transport

	poolMu sync.Mutex
	pool   []pooledConn

	batchMu      sync.Mutex
	batchPending []batchItem
	batchTimer   *time.Timer

	coalesceMu sync.Mutex
	coalesce   map[string][]chan coalesceResult

	statsStop chan struct{}
}

// coalesceResult هم مقدار هم خطا رو نگه می‌داره تا waiterها block نمونن
type coalesceResult struct {
	data []byte
	err  error
}

type pooledConn struct {
	conn    net.Conn
	created time.Time
}

type batchItem struct {
	payload map[string]any
	respCh  chan []byte
}

func New(cfg config.Config) *DomainFronter {
	frontDomain := cfg.GetString("front_domain", "www.google.com")
	fronts := buildSNIPool(frontDomain, cfg.GetStringSlice("front_domains"))
	ids := cfg.GetScriptIDs()
	if len(ids) == 0 {
		ids = []string{cfg.GetString("script_id", "")}
	}
	parallel := cfg.GetInt("parallel_relay", 1)
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(ids) {
		parallel = len(ids)
	}

	// همه IPs رو جمع‌آوری کن برای failover
	googleIPs := cfg.GetStringSlice("google_ips")
	if len(googleIPs) == 0 {
		if single := cfg.GetString("google_ip", "216.239.38.120"); single != "" {
			googleIPs = []string{single}
		} else {
			googleIPs = []string{"216.239.38.120"}
		}
	}
	connectHost := googleIPs[0]

	f := &DomainFronter{
		googleIPs:     googleIPs,
		connectHost:   connectHost,
		sniHost:       frontDomain,
		sniHosts:      fronts,
		httpHost:      "script.google.com",
		scriptIDs:     ids,
		perSite:       map[string]*HostStat{},
		authKey:       cfg.GetString("auth_key", ""),
		verifySSL:     cfg.GetBool("verify_ssl", true),
		relayTO:       time.Duration(cfg.GetInt("relay_timeout", constants.RelayTimeout)) * time.Second,
		tlsTO:         time.Duration(cfg.GetInt("tls_connect_timeout", constants.TLSConnectTimeout)) * time.Second,
		maxResp:       cfg.GetInt("max_response_body_bytes", constants.MaxResponseBodyBytes),
		parallelRelay: parallel,
		coalesce:      map[string][]chan coalesceResult{},
		statsStop:     make(chan struct{}),
	}

	if len(fronts) > 1 {
		log.Infof("SNI rotation pool (%d): %s", len(fronts), strings.Join(fronts, ", "))
	}
	if len(googleIPs) > 1 {
		log.Infof("IP failover pool (%d): %s", len(googleIPs), strings.Join(googleIPs, ", "))
	}
	if parallel > 1 {
		log.Infof("Fan-out relay: %d parallel Apps Script instances per request", parallel)
	}
	log.Infof("Response codecs: %s", codec.SupportedEncodings())

	f.h2 = h2.New(f.connectHost, f.sniHosts, f.verifySSL)

	// Pool warm-up: اتصال‌های آماده از قبل بساز
	go f.warmPool()
	go f.statsLoop()
	return f
}

// warmPool اتصال‌های اولیه pool رو از قبل می‌سازه تا اولین requestها کند نباشن
func (f *DomainFronter) warmPool() {
	count := constants.WarmPoolCount
	if count > constants.PoolMax {
		count = constants.PoolMax
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // حداکثر ۴ اتصال موازی هنگام warm-up
	for i := 0; i < count; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			conn, err := f.dialFresh()
			if err != nil {
				return
			}
			f.release(conn)
		}()
	}
	wg.Wait()
	f.poolMu.Lock()
	actual := len(f.pool)
	f.poolMu.Unlock()
	log.Infof("Pool warm-up complete: %d/%d connections ready", actual, count)
}

// nextGoogleIP برای IP failover به ترتیب round-robin IP بعدی رو برمی‌گردونه
func (f *DomainFronter) nextGoogleIP() string {
	if len(f.googleIPs) == 1 {
		return f.googleIPs[0]
	}
	idx := atomic.AddUint32(&f.googleIPIdx, 1)
	return f.googleIPs[int(idx)%len(f.googleIPs)]
}

func buildSNIPool(frontDomain string, overrides []string) []string {
	if len(overrides) > 0 {
		seen := map[string]bool{}
		out := []string{}
		for _, item := range overrides {
			host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(item), "."))
			if host != "" && !seen[host] {
				seen[host] = true
				out = append(out, host)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	fd := strings.ToLower(strings.TrimSuffix(frontDomain, "."))
	if strings.HasSuffix(fd, ".google.com") || fd == "google.com" {
		pool := []string{fd}
		for _, h := range constants.FrontSNIPoolGoogle {
			if h != fd {
				pool = append(pool, h)
			}
		}
		return pool
	}
	if fd == "" {
		return []string{"www.google.com"}
	}
	return []string{fd}
}

func (f *DomainFronter) Close() error {
	close(f.statsStop)
	if f.h2 != nil {
		_ = f.h2.Close()
	}
	f.poolMu.Lock()
	for _, pc := range f.pool {
		_ = pc.conn.Close()
	}
	f.pool = nil
	f.poolMu.Unlock()
	return nil
}

func (f *DomainFronter) Relay(method, urlStr string, headers map[string]string, body []byte) []byte {
	payload := f.buildPayload(method, urlStr, headers, body)
	start := time.Now()
	errFlag := false
	var raw []byte
	defer func() {
		f.recordSite(urlStr, len(raw), time.Since(start), errFlag)
	}()

	if f.isStatefulRequest(method, urlStr, headers, body) {
		resp, e := f.relaySingle(payload)
		if e != nil {
			errFlag = true
			return f.errorResponse(502, e.Error())
		}
		raw = resp
		return resp
	}

	key := f.coalesceKey(urlStr, headers)
	if strings.ToUpper(method) == "GET" && len(body) == 0 {
		if v := headerValue(headers, "range"); v == "" {
			if resp, ok := f.tryCoalesce(key, payload); ok {
				raw = resp
				return resp
			}
		}
	}

	resp, e := f.batchSubmit(payload)
	if e != nil {
		errFlag = true
		raw = f.errorResponse(502, e.Error())
		return raw
	}
	raw = resp
	return resp
}

func (f *DomainFronter) tryCoalesce(key string, payload map[string]any) ([]byte, bool) {
	f.coalesceMu.Lock()
	if waiters, ok := f.coalesce[key]; ok {
		ch := make(chan coalesceResult, 1)
		f.coalesce[key] = append(waiters, ch)
		f.coalesceMu.Unlock()
		// اگه timeout بخوره یا خطا بیاد، به جای block شدن، خطا برمی‌گردونه
		select {
		case res := <-ch:
			if res.err != nil {
				return f.errorResponse(502, res.err.Error()), true
			}
			return res.data, true
		case <-time.After(f.relayTO + 5*time.Second):
			return f.errorResponse(504, "coalesce timeout"), true
		}
	}
	f.coalesce[key] = []chan coalesceResult{}
	f.coalesceMu.Unlock()

	resp, err := f.batchSubmit(payload)
	if err != nil {
		resp = f.errorResponse(502, err.Error())
	}

	f.coalesceMu.Lock()
	waiters := f.coalesce[key]
	delete(f.coalesce, key)
	f.coalesceMu.Unlock()

	// همه waiterها رو آزاد کن — با خطا یا بدون خطا
	result := coalesceResult{data: resp, err: err}
	for _, ch := range waiters {
		ch <- result
	}
	return resp, true
}

func (f *DomainFronter) batchSubmit(payload map[string]any) ([]byte, error) {
	respCh := make(chan []byte, 1)
	item := batchItem{payload: payload, respCh: respCh}

	f.batchMu.Lock()
	f.batchPending = append(f.batchPending, item)
	if len(f.batchPending) >= constants.BatchMax {
		pending := f.batchPending
		f.batchPending = nil
		if f.batchTimer != nil {
			f.batchTimer.Stop()
			f.batchTimer = nil
		}
		f.batchMu.Unlock()
		go f.flushBatch(pending)
		return <-respCh, nil
	}
	if f.batchTimer == nil {
		f.batchTimer = time.AfterFunc(time.Duration(constants.BatchWindowMicro*float64(time.Second)), func() {
			f.batchMu.Lock()
			pending := f.batchPending
			f.batchPending = nil
			f.batchTimer = nil
			f.batchMu.Unlock()
			if len(pending) > 0 {
				f.flushBatch(pending)
			}
		})
	}
	f.batchMu.Unlock()
	return <-respCh, nil
}

func (f *DomainFronter) flushBatch(batch []batchItem) {
	if len(batch) == 1 {
		resp, err := f.relaySingle(batch[0].payload)
		if err != nil {
			resp = f.errorResponse(502, err.Error())
		}
		batch[0].respCh <- resp
		return
	}
	results, err := f.relayBatch(batch)
	if err != nil {
		for _, item := range batch {
			item.respCh <- f.errorResponse(502, err.Error())
		}
		return
	}
	for i, item := range batch {
		item.respCh <- results[i]
	}
}

func (f *DomainFronter) relaySingle(payload map[string]any) ([]byte, error) {
	full := map[string]any{}
	for k, v := range payload {
		full[k] = v
	}
	full["k"] = f.authKey
	jsonBody, _ := json.Marshal(full)
	path := f.execPath(payload["u"])

	_, _, body, err := f.h2.Request(context.Background(), "POST", path, f.httpHost, map[string]string{"content-type": "application/json"}, jsonBody, f.relayTO)
	if err == nil {
		return f.parseRelayResponse(body), nil
	}

	// fallback به HTTP/1 با IP failover
	resp, err := f.relayHTTP1WithFailover(path, jsonBody)
	if err != nil {
		return nil, err
	}
	return f.parseRelayResponse(resp), nil
}

func (f *DomainFronter) relayBatch(batch []batchItem) ([][]byte, error) {
	payloads := []map[string]any{}
	for _, item := range batch {
		payloads = append(payloads, item.payload)
	}
	full := map[string]any{
		"k": f.authKey,
		"q": payloads,
	}
	jsonBody, _ := json.Marshal(full)
	path := f.execPath(payloads[0]["u"])

	_, _, body, err := f.h2.Request(context.Background(), "POST", path, f.httpHost, map[string]string{"content-type": "application/json"}, jsonBody, 30*time.Second)
	if err == nil {
		return f.parseBatchBody(body, len(batch))
	}
	resp, err := f.relayHTTP1WithFailover(path, jsonBody)
	if err != nil {
		return nil, err
	}
	return f.parseBatchBody(resp, len(batch))
}

// relayHTTP1WithFailover در صورت خطا IP بعدی رو امتحان می‌کنه
func (f *DomainFronter) relayHTTP1WithFailover(path string, body []byte) ([]byte, error) {
	var lastErr error
	tried := map[string]bool{}
	for attempt := 0; attempt < len(f.googleIPs); attempt++ {
		ip := f.nextGoogleIP()
		if tried[ip] {
			continue
		}
		tried[ip] = true

		resp, err := f.relayHTTP1OnIP(ip, path, body)
		if err == nil {
			// اگه این IP کار کرد و با IP فعلی فرق داره، H2 رو هم آپدیت کن
			if ip != f.connectHost {
				f.connectHost = ip
				f.h2.UpdateHost(ip)
				log.Infof("IP failover: switched to %s", ip)
			}
			return resp, nil
		}
		lastErr = err
		log.Warnf("IP %s failed: %v", ip, err)
	}
	return nil, fmt.Errorf("all IPs failed, last error: %w", lastErr)
}

func (f *DomainFronter) relayHTTP1OnIP(ip, path string, body []byte) ([]byte, error) {
	dialer := &net.Dialer{Timeout: f.tlsTO}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	sni := f.nextSNI()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: !f.verifySSL})
	if err := tlsConn.Handshake(); err != nil {
		return nil, err
	}
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nAccept-Encoding: gzip\r\nConnection: keep-alive\r\n\r\n", path, f.httpHost, len(body))
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		return nil, err
	}
	if _, err := tlsConn.Write(body); err != nil {
		return nil, err
	}
	status, headers, respBody, err := readHTTPResponse(tlsConn, f.maxResp)
	if err != nil {
		return nil, err
	}
	if status >= 300 && status < 400 {
		loc := headers["location"]
		if loc != "" {
			parsed, _ := url.Parse(loc)
			rpath := parsed.Path
			if parsed.RawQuery != "" {
				rpath += "?" + parsed.RawQuery
			}
			return f.relayHTTP1OnIP(ip, rpath, body)
		}
	}
	return respBody, nil
}

func (f *DomainFronter) relayHTTP1(path string, body []byte) ([]byte, error) {
	conn, err := f.acquire()
	if err != nil {
		return nil, err
	}
	defer f.release(conn)

	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nAccept-Encoding: gzip\r\nConnection: keep-alive\r\n\r\n", path, f.httpHost, len(body))
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}

	status, headers, respBody, err := readHTTPResponse(conn, f.maxResp)
	if err != nil {
		return nil, err
	}

	if status >= 300 && status < 400 {
		loc := headers["location"]
		if loc != "" {
			parsed, _ := url.Parse(loc)
			rpath := parsed.Path
			if parsed.RawQuery != "" {
				rpath += "?" + parsed.RawQuery
			}
			return f.relayHTTP1(rpath, body)
		}
	}
	return respBody, nil
}

func readHTTPResponse(conn net.Conn, maxBody int) (int, map[string]string, []byte, error) {
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return 0, nil, nil, err
	}
	status := 0
	// استفاده از regex کامپایل‌شده در سطح package
	if m := statusLineRegex.FindString(statusLine); m != "" {
		status, _ = strconv.Atoi(m)
	}
	headers := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return status, headers, nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			headers[strings.ToLower(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}

	cl := 0
	if v := headers["content-length"]; v != "" {
		cl, _ = strconv.Atoi(v)
	}
	body := []byte{}
	if cl > 0 {
		if cl > maxBody {
			return status, headers, nil, errors.New("response exceeds cap")
		}
		buf := make([]byte, cl)
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			return status, headers, nil, err
		}
		body = buf
	} else {
		buf, _ := io.ReadAll(reader)
		body = buf
	}
	if enc := headers["content-encoding"]; enc != "" {
		body = codec.Decode(body, enc)
	}
	return status, headers, body, nil
}

// dialFresh یک اتصال TLS جدید می‌سازه (برای pool warm-up)
func (f *DomainFronter) dialFresh() (net.Conn, error) {
	dialer := &net.Dialer{Timeout: f.tlsTO}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(f.connectHost, "443"))
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: f.nextSNI(), InsecureSkipVerify: !f.verifySSL})
	if err := tlsConn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (f *DomainFronter) acquire() (net.Conn, error) {
	f.poolMu.Lock()
	for len(f.pool) > 0 {
		pc := f.pool[len(f.pool)-1]
		f.pool = f.pool[:len(f.pool)-1]
		if time.Since(pc.created) < time.Duration(constants.ConnTTL*float64(time.Second)) {
			f.poolMu.Unlock()
			return pc.conn, nil
		}
		_ = pc.conn.Close()
	}
	f.poolMu.Unlock()
	return f.dialFresh()
}

func (f *DomainFronter) release(conn net.Conn) {
	f.poolMu.Lock()
	defer f.poolMu.Unlock()
	if len(f.pool) >= constants.PoolMax {
		_ = conn.Close()
		return
	}
	f.pool = append(f.pool, pooledConn{conn: conn, created: time.Now()})
}

func (f *DomainFronter) nextSNI() string {
	idx := atomic.AddUint32(&f.sniIdx, 1)
	return f.sniHosts[int(idx)%len(f.sniHosts)]
}

func (f *DomainFronter) execPath(urlOrHost any) string {
	sid := f.scriptIDForKey(hostKey(fmt.Sprint(urlOrHost)))
	if f.devAvail {
		return "/macros/s/" + sid + "/dev"
	}
	return "/macros/s/" + sid + "/exec"
}

func hostKey(urlOrHost string) string {
	if urlOrHost == "" {
		return ""
	}
	if strings.Contains(urlOrHost, "://") {
		parsed, err := url.Parse(urlOrHost)
		if err == nil {
			return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		}
	}
	return strings.ToLower(strings.TrimSuffix(urlOrHost, "."))
}

func (f *DomainFronter) scriptIDForKey(key string) string {
	if len(f.scriptIDs) == 1 {
		return f.scriptIDs[0]
	}
	if key == "" {
		idx := atomic.AddUint32(&f.scriptIdx, 1)
		return f.scriptIDs[int(idx)%len(f.scriptIDs)]
	}
	h := sha1.Sum([]byte(key))
	idx := int(h[0]) % len(f.scriptIDs)
	return f.scriptIDs[idx]
}

func (f *DomainFronter) buildPayload(method, urlStr string, headers map[string]string, body []byte) map[string]any {
	p := map[string]any{
		"m": method,
		"u": urlStr,
		"r": false,
	}
	if headers != nil {
		filtered := make(map[string]string)
		for k, v := range headers {
			if strings.ToLower(k) != "accept-encoding" {
				filtered[k] = v
			}
		}
		p["h"] = filtered
	}
	if len(body) > 0 {
		p["b"] = base64.StdEncoding.EncodeToString(body)
		if ct := headerValue(headers, "content-type"); ct != "" {
			p["ct"] = ct
		}
	}
	return p
}

func (f *DomainFronter) parseRelayResponse(body []byte) []byte {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return f.errorResponse(502, "Empty response from relay")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		m := regexp.MustCompile(`\{.*\}`).FindString(text)
		if m == "" {
			return f.errorResponse(502, "No JSON: "+truncate(text, 200))
		}
		if err := json.Unmarshal([]byte(m), &data); err != nil {
			return f.errorResponse(502, "Bad JSON: "+truncate(text, 200))
		}
	}
	return f.parseRelayJSON(data)
}

func (f *DomainFronter) errorResponse(status int, message string) []byte {
	body := fmt.Sprintf("<html><body><h1>%d</h1><p>%s</p></body></html>", status, message)
	resp := fmt.Sprintf("HTTP/1.1 %d Error\r\nContent-Type: text/html\r\nContent-Length: %d\r\n\r\n%s", status, len(body), body)
	return []byte(resp)
}

func (f *DomainFronter) parseRelayJSON(data map[string]any) []byte {
	if e, ok := data["e"]; ok {
		return f.errorResponse(502, fmt.Sprintf("Relay error: %v", e))
	}
	status := intVal(data["s"], 200)
	headers := map[string]any{}
	if h, ok := data["h"].(map[string]any); ok {
		headers = h
	}
	bodyRaw := ""
	if b, ok := data["b"].(string); ok {
		bodyRaw = b
	}
	body, _ := base64.StdEncoding.DecodeString(bodyRaw)
	if len(body) > f.maxResp {
		return f.errorResponse(502, "Relay response exceeds cap")
	}
	statusText := "OK"
	switch status {
	case 206:
		statusText = "Partial Content"
	case 301:
		statusText = "Moved"
	case 302:
		statusText = "Found"
	case 304:
		statusText = "Not Modified"
	case 400:
		statusText = "Bad Request"
	case 403:
		statusText = "Forbidden"
	case 404:
		statusText = "Not Found"
	case 500:
		statusText = "Internal Server Error"
	}

	buf := bytes.NewBufferString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", status, statusText))
	skip := map[string]bool{
		"transfer-encoding": true,
		"connection":        true,
		"keep-alive":        true,
		"content-length":    true,
		"content-encoding":  true,
	}
	for k, v := range headers {
		lk := strings.ToLower(k)
		if skip[lk] {
			continue
		}
		switch val := v.(type) {
		case []any:
			for _, item := range val {
				buf.WriteString(fmt.Sprintf("%s: %v\r\n", k, item))
			}
		default:
			buf.WriteString(fmt.Sprintf("%s: %v\r\n", k, val))
		}
	}
	buf.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)))
	buf.Write(body)
	return buf.Bytes()
}

func (f *DomainFronter) parseBatchBody(body []byte, expected int) ([][]byte, error) {
	text := strings.TrimSpace(string(body))
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		return nil, err
	}
	if e, ok := data["e"]; ok {
		return nil, fmt.Errorf("Batch error: %v", e)
	}
	arr, ok := data["q"].([]any)
	if !ok || len(arr) != expected {
		return nil, errors.New("batch size mismatch")
	}
	results := make([][]byte, 0, len(arr))
	for _, item := range arr {
		if obj, ok := item.(map[string]any); ok {
			results = append(results, f.parseRelayJSON(obj))
		}
	}
	return results, nil
}

func (f *DomainFronter) isStatefulRequest(method, urlStr string, headers map[string]string, body []byte) bool {
	method = strings.ToUpper(method)
	if method != "GET" && method != "HEAD" {
		return true
	}
	if len(body) > 0 {
		return true
	}
	for _, name := range constants.StatefulHeaderNames {
		if headerValue(headers, name) != "" {
			return true
		}
	}
	accept := strings.ToLower(headerValue(headers, "accept"))
	if strings.Contains(accept, "text/html") || strings.Contains(accept, "application/json") {
		return true
	}
	fetchMode := strings.ToLower(headerValue(headers, "sec-fetch-mode"))
	if fetchMode == "navigate" || fetchMode == "cors" {
		return true
	}
	return !isStaticAssetURL(urlStr)
}

func isStaticAssetURL(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	for _, ext := range constants.StaticExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func (f *DomainFronter) coalesceKey(urlStr string, headers map[string]string) string {
	key := []string{urlStr}
	if headers != nil {
		for _, name := range []string{"accept", "accept-language", "user-agent", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site"} {
			if v := headerValue(headers, name); v != "" {
				key = append(key, name+"="+v)
			}
		}
	}
	return strings.Join(key, "\n")
}

func (f *DomainFronter) recordSite(urlStr string, bytes int, latency time.Duration, errored bool) {
	host := hostKey(urlStr)
	if host == "" {
		return
	}
	f.perSiteMu.Lock()
	defer f.perSiteMu.Unlock()
	stat, ok := f.perSite[host]
	if !ok {
		stat = &HostStat{}
		f.perSite[host] = stat
	}
	stat.Requests++
	stat.Bytes += bytes
	stat.TotalLatencyNs += latency.Nanoseconds()
	if errored {
		stat.Errors++
	}
}

func (f *DomainFronter) statsLoop() {
	ticker := time.NewTicker(time.Duration(constants.StatsLogInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-f.statsStop:
			return
		case <-ticker.C:
			f.logStats()
		}
	}
}

func (f *DomainFronter) logStats() {
	f.perSiteMu.RLock()
	if len(f.perSite) == 0 {
		f.perSiteMu.RUnlock()
		return
	}
	type statEntry struct {
		host string
		stat *HostStat
	}
	entries := make([]statEntry, 0, len(f.perSite))
	for host, stat := range f.perSite {
		entries = append(entries, statEntry{host: host, stat: stat})
	}
	f.perSiteMu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].stat.Bytes > entries[j].stat.Bytes
	})
	count := constants.StatsLogTopN
	if count > len(entries) {
		count = len(entries)
	}
	log.Debugf("-- Per-host stats (top %d by bytes) --", count)
	for i := 0; i < count; i++ {
		e := entries[i]
		avgLatency := time.Duration(0)
		if e.stat.Requests > 0 {
			avgLatency = time.Duration(e.stat.TotalLatencyNs / int64(e.stat.Requests))
		}
		log.Debugf("  %s: %d reqs, %.2fMB, %s avg, %d errs",
			e.host, e.stat.Requests, float64(e.stat.Bytes)/1024/1024, avgLatency, e.stat.Errors)
	}
}

func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.ToLower(k) == name {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if i, err := strconv.Atoi(t); err == nil {
			return i
		}
	}
	return def
}