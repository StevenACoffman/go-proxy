package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/icio/mkcert"
)

func main() {
	origin, err := url.Parse("https://postman-echo.com/")
	path := "/something/"
	if err != nil {
		panic(err)
	}
	port := flag.Int("p", 80, "port")
	flag.Parse()

	director := func(req *http.Request) {
		req.URL.Scheme = origin.Scheme
		req.URL.Host = origin.Host
		req.Header.Add("X-Forwarded-Host", req.Host)
		req.Header.Add("X-Origin-Host", origin.Host)
		proxyPath := ""
		if req.URL.Path != "" && len(req.URL.Path) > len(path) {
			proxyPath = singleJoiningSlash(origin.Path, req.URL.Path[len(path):])
			if strings.HasSuffix(proxyPath, "/") && len(proxyPath) > 1 {
				proxyPath = proxyPath[:len(proxyPath)-1]
			}
		}
		req.URL.Path = proxyPath

	}

	reverseProxy := &httputil.ReverseProxy{Director: director}
	handler := handler{proxy: reverseProxy}

	http.Handle(path, http.StripPrefix(path, handler))
	//http.Handle("/", handler)


	if *port == 443 {
		_, tlsPort, err := net.SplitHostPort(":443")
		if err != nil {
			log.Println(err)
		}
		go redirectToHTTPS(tlsPort)
		path, err := os.Getwd()
		if err != nil {
			log.Println(err)
		}
		certFileName := "localhost-cert.pem"
		certKeyFileName := "localhost-key.pem"

		if !fileExists(certFileName) || !fileExists(certKeyFileName) {
			_, err := mkcert.Exec(
				// Domains tells mkcert what certificate to generate.
				mkcert.Domains("localhost"),
				// RequireTrusted(true) tells Exec to return an error if the CA isn't
				// in the trust stores.
				mkcert.RequireTrusted(true),
				// CertFile and KeyFile override the default behaviour of generating
				// the keys in the local directory.
				mkcert.CertFile(filepath.Join(path, certFileName)),
				mkcert.KeyFile(filepath.Join(path, certKeyFileName)),
			)
			if err != nil {
				log.Println(err)
			}
		}

		http.ListenAndServeTLS(fmt.Sprintf(":%d", *port), certFileName, certKeyFileName, handler)
	} else {
		http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
	}
}

func redirectToHTTPS(tlsPort string) {
	httpSrv := http.Server{
		Addr: ":80",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
			host, _, _ := net.SplitHostPort(r.Host)
			if host == "" {
				host = "localhost"
			}
			u := r.URL
			u.Host = net.JoinHostPort(host, tlsPort)
			u.Scheme="https"
			log.Println(u.String())
			http.Redirect(w,r,u.String(), http.StatusFound)
		}),
	}
	log.Println(httpSrv.ListenAndServe())
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

type handler struct {
	proxy *httputil.ReverseProxy
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for name, _ := range r.Header {
		if strings.ToLower(name) == "set-cookie" {
			// For example keep one domain unchanged, rewrite one domain and remove other domains
			cookieConfig := make(map[string]string)
			cookieConfig["unchanged.domain"] = "unchanged.domain"
			cookieConfig["old.domain"] = "new.domain"
			cookieConfig["google.com"] = "localhost"
			cookieConfig["www.google.com"] = "localhost"
			cookieConfig[".google.com"] = "localhost"
			//remove other cookies
			cookieConfig["*"] = ""
			r.Header.Set(name, rewriteCookieDomain(r.Header.Get(name), cookieConfig))
		}
	}
	if r.Header.Get("X-Forwarded-Host") == "" {
		r.Header.Set("X-Forwarded-Host", r.Host)
	}
	if r.Header.Get("X-Forwarded-Proto") == "" {
		r.Header.Set("X-Forwarded-Proto", r.URL.Scheme)
	}
	r.Host = r.URL.Host
	//if targetQuery == "" || r.URL.RawQuery == "" {
	//	r.URL.RawQuery = targetQuery + r.URL.RawQuery
	//} else {
	//	r.URL.RawQuery = targetQuery + "&" + r.URL.RawQuery
	//}
	h.proxy.ServeHTTP(w, r)
}


// https://gist.github.com/elliotchance/d419395aa776d632d897
func ReplaceAllStringSubmatchFunc(re *regexp.Regexp, str string, repl func([]string) string) string {
	result := ""
	lastIndex := 0

	for _, v := range re.FindAllSubmatchIndex([]byte(str), -1) {
		groups := []string{}
		for i := 0; i < len(v); i += 2 {
			groups = append(groups, str[v[i]:v[i+1]])
		}

		result += str[lastIndex:v[0]] + repl(groups)
		lastIndex = v[1]
	}

	return result + str[lastIndex:]
}

/* config is mapping of domains to new domains, use "*" to match all domains.
For example keep one domain unchanged, rewrite one domain and remove other domains:
cookieDomainRewrite: {
  "unchanged.domain": "unchanged.domain",
  "old.domain": "new.domain",
  "*": ""
*/
func rewriteCookieDomain(header string, config map[string]string) string {

	re := regexp.MustCompile(`(?i)(\s*; Domain=)([^;]+)`)
	return ReplaceAllStringSubmatchFunc(re, header, func(groups []string) string {
		match, prefix, previousValue := groups[0], groups[1], groups[2]

		var newValue string
		if config[previousValue] != "" {
			newValue = config[previousValue]
		} else if config["*"] != "" {
			newValue = config["*"]
		} else {
			//no match, return previous value
			return match
		}
		if newValue != "" {
			//replace value
			return prefix + newValue
		} else {
			//remove value
			return ""
		}
	})
}
