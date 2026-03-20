package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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
	configPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		configPath = p
	}

	if _, err := LoadConfig(configPath); err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}

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

	log.Printf("HTTPS server listening on %s\n", c.HTTPSPort)
	if err := server.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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
