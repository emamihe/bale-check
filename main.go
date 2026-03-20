package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
)

type safeLogger struct {
	mu sync.Mutex
}

func (l *safeLogger) log(format string, v ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Printf(format, v...)
}

var slog = &safeLogger{}

var (
	workingCountries   []string
	workingCountriesMu sync.RWMutex
)

var countryCodes = []string{
	"AF", "AL", "DZ", "AS", "AD", "AO", "AI", "AQ", "AG", "AR", "AM", "AW", "AU", "AT", "AZ",
	"BS", "BH", "BD", "BB", "BY", "BE", "BZ", "BJ", "BM", "BT", "BO", "BA", "BW", "BR", "IO",
	"BN", "BG", "BF", "BI", "CV", "KH", "CM", "CA", "KY", "CF", "TD", "CL", "CN", "CO", "KM",
	"CD", "CG", "KR", "CR", "CI", "HR", "CU", "CY", "CZ", "DK", "DJ", "DM", "DO", "EC", "EG",
	"SV", "GQ", "ER", "EE", "SZ", "ET", "FJ", "FI", "FR", "GA", "GM", "GE", "DE", "GH", "GI",
	"GR", "GD", "GT", "GN", "GW", "GY", "HT", "HN", "HK", "HU", "IS", "IN", "ID", "IR", "IQ",
	"IE", "IL", "IT", "JM", "JP", "JO", "KZ", "KE", "KI", "KP", "KW", "KG", "LA", "LV", "LB",
	"LS", "LR", "LY", "LI", "LT", "LU", "MO", "MG", "MW", "MY", "MV", "ML", "MT", "MH", "MR",
	"MU", "MX", "FM", "MD", "MC", "MN", "ME", "MA", "MZ", "MM", "NA", "NR", "NP", "NL", "NZ",
	"NI", "NE", "NG", "MK", "NO", "OM", "PK", "PW", "PS", "PA", "PG", "PY", "PE", "PH", "PL",
	"PT", "PR", "QA", "RO", "RU", "RW", "KN", "LC", "VC", "WS", "SM", "ST", "SA", "SN", "RS",
	"SC", "SL", "SG", "SK", "SI", "SB", "SO", "ZA", "SS", "ES", "LK", "SD", "SR", "SE", "CH",
	"SY", "TW", "TJ", "TZ", "TH", "TL", "TG", "TO", "TT", "TN", "TR", "TM", "TV", "UG", "UA",
	"AE", "GB", "US", "UY", "UZ", "VU", "VE", "VN", "YE", "ZM", "ZW",
}

func main() {
	var configPath string
	var forwardProxy bool

	rootCmd := &cobra.Command{
		Use:   "bale-check",
		Short: "Bale Messenger countries check - tests proxy connectivity and exposes results via HTTPS API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := LoadConfig(configPath); err != nil {
				return err
			}
			if cmd.Flags().Changed("forward-proxy") {
				GetConfig().ForwardProxyEnabled = forwardProxy
			}
			return runApp()
		},
	}

	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "path to config file (required)")
	rootCmd.Flags().BoolVar(&forwardProxy, "forward-proxy", false, "enable HTTP forward proxy (upstream_proxy_url, upstream_proxy_insecure from config)")
	rootCmd.MarkFlagRequired("config")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func runApp() error {
	c := GetConfig()
	p := GetParsedConfig()

	log.Println("Starting Bale Messenger countries check service...")
	log.Printf("Target URL: %s\n", c.TargetURL)
	log.Printf("Check interval: %v\n", p.CheckInterval)
	log.Printf("API: https://localhost%s/countries\n", c.HTTPSPort)

	runCheck()

	go func() {
		ticker := time.NewTicker(p.CheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			runCheck()
		}
	}()

	router := setupRouter()
	server := &http.Server{
		Addr:      c.HTTPSPort,
		Handler:   router,
		TLSConfig: generateTLSConfig(),
	}

	if c.ForwardProxyEnabled {
		if c.UpstreamProxyURL == "" {
			return fmt.Errorf("forward proxy enabled but no upstream proxy configured (set upstream_proxy_url in config)")
		}
		proxyHandler := newForwardProxyHandler(c.UpstreamProxyURL, p.RequestTimeout, c.UpstreamProxyInsecure)
		proxyServer := &http.Server{
			Addr: c.ForwardProxyPort,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodConnect {
					proxyHandler.handleTunneling(w, r)
				} else {
					http.Error(w, "Only CONNECT method supported", http.StatusMethodNotAllowed)
				}
			}),
			// Disable HTTP/2 for CONNECT hijacking
			TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
		}
		log.Printf("HTTP forward proxy listening on %s (upstream: %s)\n", c.ForwardProxyPort, maskProxyURL(c.UpstreamProxyURL))
		go func() {
			if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("Forward proxy error: %v\n", err)
			}
		}()
	}

	log.Printf("HTTPS server listening on %s\n", c.HTTPSPort)
	return server.ListenAndServeTLS("", "")
}

func runCheck() {
	c := GetConfig()
	p := GetParsedConfig()

	slog.log("----------------------------------------")
	slog.log("Starting country check run...\n")

	resultsCh := make(chan string, c.ConcurrentWorkers*2)
	countryCh := make(chan string, len(countryCodes))

	var wg sync.WaitGroup
	for i := 0; i < c.ConcurrentWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for countryCode := range countryCh {
				proxyURL, err := buildProxyURL(c, countryCode)
				if err != nil {
					slog.log("[%s] Failed to build proxy URL: %v\n", countryCode, err)
					continue
				}

				client := createHTTPClient(proxyURL, p.RequestTimeout)
				resp, err := makeRequest(client, c.TargetURL)
				if err != nil {
					if isTimeout(err) {
						slog.log("[%s] Timeout - skipping\n", countryCode)
					} else {
						slog.log("[%s] Request failed: %v\n", countryCode, err)
					}
					continue
				}
				resp.Body.Close()

				if resp.StatusCode == 401 {
					slog.log("[%s] SUCCESS - Returned 401 (country works!)\n", countryCode)
					resultsCh <- countryCode
				} else {
					slog.log("[%s] Got HTTP %d (not 401)\n", countryCode, resp.StatusCode)
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	for _, countryCode := range countryCodes {
		countryCh <- countryCode
	}
	close(countryCh)

	var results []string
	for code := range resultsCh {
		results = append(results, code)
	}

	workingCountriesMu.Lock()
	workingCountries = results
	workingCountriesMu.Unlock()

	slog.log("Check complete. %d countries returned 401.\n", len(results))
	slog.log("----------------------------------------\n")
}

func setupRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/countries", func(c *gin.Context) {
		workingCountriesMu.RLock()
		codes := make([]string, len(workingCountries))
		copy(codes, workingCountries)
		workingCountriesMu.RUnlock()

		names := make([]string, 0, len(codes))
		for _, code := range codes {
			if name, ok := countryCodeToName[code]; ok {
				names = append(names, name)
			} else {
				names = append(names, code)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"countries": names,
			"count":     len(names),
		})
	})

	return router
}

func generateTLSConfig() *tls.Config {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Bale Countries Check"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		log.Fatalf("Failed to create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		log.Fatalf("Failed to parse certificate: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{cert.Raw},
			PrivateKey:  key,
		}},
	}
}

func buildProxyURL(c *Config, countryCode string) (*url.URL, error) {
	username := fmt.Sprintf("%s-%s-rotate", c.Proxy.Username, countryCode)
	proxyURLStr := fmt.Sprintf("http://%s@%s:%s",
		url.UserPassword(username, c.Proxy.Password).String(),
		c.Proxy.Host, c.Proxy.Port)
	return url.Parse(proxyURLStr)
}

func createHTTPClient(proxyURL *url.URL, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

func makeRequest(client *http.Client, targetURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	type timeout interface {
		Timeout() bool
	}
	if t, ok := err.(timeout); ok && t.Timeout() {
		return true
	}
	return err.Error() == "context deadline exceeded" ||
		err.Error() == "net/http: request canceled (Client.Timeout exceeded)"
}

// forwardProxyHandler handles CONNECT requests and tunnels via upstream HTTP proxy.
type forwardProxyHandler struct {
	upstreamProxyURL      string
	timeout               time.Duration
	upstreamProxyInsecure bool
}

func newForwardProxyHandler(upstreamProxyURL string, timeout time.Duration, insecure bool) *forwardProxyHandler {
	return &forwardProxyHandler{
		upstreamProxyURL:      upstreamProxyURL,
		timeout:               timeout,
		upstreamProxyInsecure: insecure,
	}
}

func (h *forwardProxyHandler) handleTunneling(w http.ResponseWriter, r *http.Request) {
	destConn, err := h.dialViaUpstream(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer destConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection established to client
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	go transfer(destConn, clientConn)
	transfer(clientConn, destConn)
}

func (h *forwardProxyHandler) dialViaUpstream(target string) (net.Conn, error) {
	upstreamURL, err := url.Parse(h.upstreamProxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream proxy URL: %w", err)
	}

	proxyHost := upstreamURL.Host
	if upstreamURL.Port() == "" {
		if upstreamURL.Scheme == "https" {
			proxyHost = net.JoinHostPort(upstreamURL.Hostname(), "443")
		} else {
			proxyHost = net.JoinHostPort(upstreamURL.Hostname(), "80")
		}
	}

	var conn net.Conn
	if upstreamURL.Scheme == "https" {
		tlsCfg := &tls.Config{InsecureSkipVerify: h.upstreamProxyInsecure}
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: h.timeout}, "tcp", proxyHost, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", proxyHost, h.timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("dial upstream proxy: %w", err)
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if upstreamURL.User != nil {
		pass, _ := upstreamURL.User.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(upstreamURL.User.Username() + ":" + pass))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
	}
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write CONNECT to upstream: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("upstream proxy returned %d", resp.StatusCode)
	}

	// Connection established; any buffered data (e.g. TLS handshake) must be forwarded
	if n := br.Buffered(); n > 0 {
		leftover := make([]byte, n)
		io.ReadFull(br, leftover)
		conn = &connWithBuffered{Conn: conn, reader: io.MultiReader(bytes.NewReader(leftover), conn)}
	}
	return conn, nil
}

type connWithBuffered struct {
	net.Conn
	reader io.Reader
}

func (c *connWithBuffered) Read(p []byte) (n int, err error) {
	return c.reader.Read(p)
}

func transfer(destination io.WriteCloser, source io.ReadCloser) {
	defer destination.Close()
	defer source.Close()
	io.Copy(destination, source)
}

func maskProxyURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return "***"
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword("***", "***")
	}
	return parsed.String()
}
