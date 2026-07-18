package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	defaultVerificationBodyBytes = 64 * 1024
	loginRedirectLimit           = 10
	loginSnippetBytes            = 300
)

var (
	genericLoginCookieNames = map[string]bool{
		"aspsessionid": true,
		"phpsessid":    true,
		"jsessionid":   true,
		"cfid":         true,
		"cftoken":      true,
	}
	loginSuccessTexts = []string{"logout", "log out", "dashboard", "welcome,", "로그아웃", "대시보드"}
	loginFailureTexts = []string{"incorrect", "invalid", "failed", "wrong", "틀렸", "잘못된"}
)

// FindingSpec is a model-declared, framework-executed verification request.
type FindingSpec struct {
	VulnType         VulnType
	Method           string
	URL              string
	BaselineURL      string
	Body             string
	BaselineBody     string
	Payload          string
	Severity         string
	Description      string
	Headers          map[string]string
	LoginURL         string
	LoginMethod      string
	LoginBody        string
	LoginContentType string
	Username         string
}

// HTTPVerifier independently collects target responses before scoring them.
type HTTPVerifier struct {
	client        *http.Client
	scope         Scope
	reproductions int
	maxBodyBytes  int
}

// loginOutcome contains the framework's deterministic login-session evidence.
// SessionCookieHeader is process-local and must never be persisted or reported.
type loginOutcome struct {
	Attempted           bool
	Verified            bool
	StatusCode          int
	CookieNames         []string
	MeaningfulCookie    bool
	SuccessText         bool
	FailText            bool
	RedirectAway        bool
	Snippet             string
	SessionCookieHeader string
	Error               string
}

// VerificationRecord captures the framework-owned HTTP exchanges used to
// reach a verification verdict.
type VerificationRecord struct {
	Method                string            `json:"method"`
	PayloadURL            string            `json:"payload_url"`
	BaselineURL           string            `json:"baseline_url,omitempty"`
	RequestHeaders        map[string]string `json:"request_headers,omitempty"`
	RequestBody           string            `json:"request_body,omitempty"`
	BaselineRequestBody   string            `json:"baseline_request_body,omitempty"`
	PayloadStatus         int               `json:"payload_status,omitempty"`
	PayloadResponseBody   string            `json:"payload_response_body,omitempty"`
	PayloadLocation       string            `json:"payload_location,omitempty"`
	BaselineStatus        int               `json:"baseline_status,omitempty"`
	BaselineResponseBody  string            `json:"baseline_response_body,omitempty"`
	Reproductions         int               `json:"reproductions,omitempty"`
	ScopeRejected         bool              `json:"scope_rejected,omitempty"`
	LoginAttempted        bool              `json:"login_attempted,omitempty"`
	LoginVerified         bool              `json:"login_verified,omitempty"`
	LoginStatus           int               `json:"login_status,omitempty"`
	LoginCookieNames      []string          `json:"login_cookie_names,omitempty"`
	LoginMeaningfulCookie bool              `json:"login_meaningful_cookie,omitempty"`
	LoginSnippet          string            `json:"login_snippet,omitempty"`
}

// NewHTTPVerifier creates a verifier that does not follow redirects so redirect
// verdicts are based on the target's own Location response.
func NewHTTPVerifier(client *http.Client, scope Scope, reproductions int) *HTTPVerifier {
	if client == nil {
		client = &http.Client{}
	}
	cloned := *client
	if cloned.Timeout <= 0 {
		cloned.Timeout = 15 * time.Second
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if reproductions <= 0 {
		reproductions = 3
	}
	return &HTTPVerifier{
		client:        &cloned,
		scope:         scope,
		reproductions: reproductions,
		maxBodyBytes:  defaultVerificationBodyBytes,
	}
}

// Verify issues framework-owned baseline and payload requests, then scores the
// bytes returned by the target. It never upgrades request failures to a finding.
func (verifier *HTTPVerifier) Verify(ctx context.Context, spec FindingSpec) VerificationResult {
	result, _ := verifier.VerifyWithEvidence(ctx, spec)
	return result
}

// VerifyWithEvidence issues framework-owned baseline and payload requests,
// returning both the deterministic verdict and the captured request/response
// record used to reach it.
func (verifier *HTTPVerifier) VerifyWithEvidence(ctx context.Context, spec FindingSpec) (VerificationResult, VerificationRecord) {
	record := newVerificationRecord(spec)
	if verifier == nil || verifier.client == nil {
		return inconclusiveResult(spec, "verifier is not configured"), record
	}
	if spec.VulnType == VulnCredential {
		return verifier.verifyCredential(ctx, spec, record)
	}
	payloadURL, err := parseVerificationURL(spec.URL)
	if err != nil {
		return inconclusiveResult(spec, "payload URL: "+err.Error()), record
	}
	record.PayloadURL = payloadURL.String()
	if !verifier.scope.HostAllowed(payloadURL.Hostname()) {
		record.ScopeRejected = true
		return inconclusiveResult(spec, "scope: host out of authorized range"), record
	}

	method := normalizedHTTPMethod(spec.Method)
	record.Method = method
	if !supportedVerificationMethod(method) {
		return inconclusiveResult(spec, "unsupported HTTP method "+method), record
	}
	login := loginOutcome{}
	if strings.TrimSpace(spec.LoginURL) != "" {
		login = verifier.verifyLogin(ctx, spec)
		record.applyLoginOutcome(login)
	}
	payloadHeaders := cloneStringMap(spec.Headers)
	if login.Verified && login.SessionCookieHeader != "" {
		if payloadHeaders == nil {
			payloadHeaders = make(map[string]string)
		}
		payloadHeaders["Cookie"] = login.SessionCookieHeader
	}
	baseline := httpVerificationResponse{}
	if strings.TrimSpace(spec.BaselineURL) != "" {
		baselineURL, err := parseVerificationURL(spec.BaselineURL)
		if err != nil {
			return inconclusiveResult(spec, "baseline URL: "+err.Error()), record
		}
		record.BaselineURL = baselineURL.String()
		if !verifier.scope.HostAllowed(baselineURL.Hostname()) {
			record.ScopeRejected = true
			return inconclusiveResult(spec, "scope: host out of authorized range"), record
		}
		record.RequestHeaders = cloneStringMap(spec.Headers)
		record.RequestBody = truncateBytes(spec.Body, verifier.maxBodyBytes)
		// A baseline would be a second non-idempotent request, so only GET/HEAD
		// obtain one after the declaration has passed the same scope check.
		if isIdempotentMethod(method) {
			record.BaselineRequestBody = truncateBytes(spec.BaselineBody, verifier.maxBodyBytes)
			baseline, err = verifier.request(ctx, method, baselineURL.String(), spec.BaselineBody, spec.Headers)
			if err != nil {
				return inconclusiveResult(spec, "baseline request: "+err.Error()), record
			}
			record.BaselineStatus = baseline.StatusCode
			record.BaselineResponseBody = baseline.Body
		}
	} else {
		record.RequestHeaders = cloneStringMap(spec.Headers)
		record.RequestBody = truncateBytes(spec.Body, verifier.maxBodyBytes)
	}

	repeats := 1
	if isIdempotentMethod(method) {
		repeats = verifier.reproductions
	}
	payload := httpVerificationResponse{}
	for attempt := 0; attempt < repeats; attempt++ {
		payload, err = verifier.request(ctx, method, payloadURL.String(), spec.Body, payloadHeaders)
		if err != nil {
			return inconclusiveResult(spec, "payload request: "+err.Error()), record
		}
		record.Reproductions++
	}
	record.PayloadStatus = payload.StatusCode
	record.PayloadResponseBody = payload.Body
	record.PayloadLocation = payload.Location

	result := Score(Evidence{
		VulnType:          spec.VulnType,
		Payload:           spec.Payload,
		ResponseBody:      payload.Body,
		BaselineBody:      baseline.Body,
		LocationHeader:    payload.Location,
		TargetHost:        verifier.scope.targetHost,
		StatusCode:        payload.StatusCode,
		BaselineStatus:    baseline.StatusCode,
		ReproductionCount: repeats,
		LoginVerified:     login.Verified,
	})
	result.applyLoginMetadata(login, spec.Username)
	result.Curl = CurlCommand(spec)
	if login.Attempted && !login.Verified {
		result.ChecksFailed = append(result.ChecksFailed, "auth session not established")
	}
	if !isIdempotentMethod(method) {
		result.ChecksFailed = append(result.ChecksFailed, "P1 replay limited to one non-idempotent request")
		result.Summary += "; non-idempotent request sent once"
	}
	return result, record
}

func (verifier *HTTPVerifier) verifyCredential(ctx context.Context, spec FindingSpec, record VerificationRecord) (VerificationResult, VerificationRecord) {
	loginURL, err := parseVerificationURL(spec.LoginURL)
	if err != nil {
		return inconclusiveResult(spec, "login URL: "+err.Error()), record
	}
	record.PayloadURL = loginURL.String()
	if !verifier.scope.HostAllowed(loginURL.Hostname()) {
		record.ScopeRejected = true
		return inconclusiveResult(spec, "scope: host out of authorized range"), record
	}
	method := normalizedLoginMethod(spec.LoginMethod)
	record.Method = method
	if !supportedVerificationMethod(method) {
		return inconclusiveResult(spec, "unsupported login HTTP method "+method), record
	}

	login := verifier.verifyLogin(ctx, spec)
	record.applyLoginOutcome(login)
	if login.Error == "" {
		record.Reproductions = 1
	}
	record.PayloadStatus = login.StatusCode
	record.PayloadResponseBody = login.Snippet

	result := Score(Evidence{
		VulnType:          spec.VulnType,
		Payload:           spec.Payload,
		ResponseBody:      login.Snippet,
		TargetHost:        verifier.scope.targetHost,
		StatusCode:        login.StatusCode,
		ReproductionCount: record.Reproductions,
		LoginVerified:     login.Verified,
	})
	result.applyLoginMetadata(login, spec.Username)
	result.Curl = loginCurlCommand(spec)
	if !login.Verified {
		result.ChecksFailed = append(result.ChecksFailed, "auth session not established")
	}
	result.ChecksFailed = append(result.ChecksFailed, "P1 replay limited to one login request")
	result.Summary += "; login request sent once"
	return result, record
}

func newVerificationRecord(spec FindingSpec) VerificationRecord {
	if spec.VulnType == VulnCredential {
		return VerificationRecord{
			Method:     normalizedLoginMethod(spec.LoginMethod),
			PayloadURL: spec.LoginURL,
		}
	}
	return VerificationRecord{
		Method:      normalizedHTTPMethod(spec.Method),
		PayloadURL:  spec.URL,
		BaselineURL: spec.BaselineURL,
	}
}

func (record *VerificationRecord) applyLoginOutcome(outcome loginOutcome) {
	record.LoginAttempted = outcome.Attempted
	record.LoginVerified = outcome.Verified
	record.LoginStatus = outcome.StatusCode
	record.LoginCookieNames = append([]string(nil), outcome.CookieNames...)
	record.LoginMeaningfulCookie = outcome.MeaningfulCookie
	record.LoginSnippet = outcome.Snippet
}

func (result *VerificationResult) applyLoginMetadata(outcome loginOutcome, username string) {
	if result == nil || !outcome.Attempted {
		return
	}
	result.LoginAttempted = true
	result.LoginVerified = outcome.Verified
	result.LoginStatus = outcome.StatusCode
	result.LoginCookieNames = append([]string(nil), outcome.CookieNames...)
	result.LoginMeaningfulCookie = outcome.MeaningfulCookie
	result.Username = username
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type httpVerificationResponse struct {
	Body       string
	StatusCode int
	Location   string
}

func (verifier *HTTPVerifier) request(ctx context.Context, method, rawURL, body string, headers map[string]string) (httpVerificationResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(body))
	if err != nil {
		return httpVerificationResponse{}, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := verifier.client.Do(request)
	if err != nil {
		return httpVerificationResponse{}, err
	}
	defer response.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, int64(verifier.maxBodyBytes)+1))
	if err != nil {
		return httpVerificationResponse{}, err
	}
	if len(bodyBytes) > verifier.maxBodyBytes {
		bodyBytes = bodyBytes[:verifier.maxBodyBytes]
	}
	return httpVerificationResponse{
		Body:       string(bodyBytes),
		StatusCode: response.StatusCode,
		Location:   response.Header.Get("Location"),
	}, nil
}

func (verifier *HTTPVerifier) verifyLogin(ctx context.Context, spec FindingSpec) loginOutcome {
	outcome := loginOutcome{Attempted: true}
	if verifier == nil || verifier.client == nil {
		outcome.Error = "verifier is not configured"
		return outcome
	}
	loginURL, err := parseVerificationURL(spec.LoginURL)
	if err != nil {
		outcome.Error = "login URL: " + err.Error()
		return outcome
	}
	if !verifier.scope.HostAllowed(loginURL.Hostname()) {
		outcome.Error = "scope: login host out of authorized range"
		return outcome
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		outcome.Error = "cookie jar: " + err.Error()
		return outcome
	}
	client := *verifier.client
	client.Jar = jar
	client.CheckRedirect = verifier.loginRedirectPolicy(&outcome)

	method := normalizedLoginMethod(spec.LoginMethod)
	if !supportedVerificationMethod(method) {
		outcome.Error = "unsupported login HTTP method " + method
		return outcome
	}
	contentType := strings.TrimSpace(spec.LoginContentType)
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	request, err := http.NewRequestWithContext(ctx, method, loginURL.String(), strings.NewReader(spec.LoginBody))
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	defer response.Body.Close()

	outcome.StatusCode = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(verifier.maxBodyBytes)+1))
	if err != nil {
		outcome.Error = err.Error()
		return outcome
	}
	if len(body) > verifier.maxBodyBytes {
		body = body[:verifier.maxBodyBytes]
	}
	outcome.Snippet = truncateBytes(string(body), loginSnippetBytes)
	outcome.SuccessText = containsLoginText(outcome.Snippet, loginSuccessTexts)
	outcome.FailText = containsLoginText(outcome.Snippet, loginFailureTexts)

	sessionURL := loginSessionURL(spec, loginURL)
	cookies := jar.Cookies(sessionURL)
	outcome.CookieNames, outcome.SessionCookieHeader = cookieEvidence(cookies)
	for _, name := range outcome.CookieNames {
		if !genericLoginCookieNames[strings.ToLower(name)] {
			outcome.MeaningfulCookie = true
			break
		}
	}
	outcome.Verified = !outcome.FailText && (outcome.SuccessText || outcome.RedirectAway) && (outcome.MeaningfulCookie || outcome.RedirectAway)
	return outcome
}

func (verifier *HTTPVerifier) loginRedirectPolicy(outcome *loginOutcome) func(*http.Request, []*http.Request) error {
	return func(request *http.Request, via []*http.Request) error {
		if len(via) >= loginRedirectLimit {
			return fmt.Errorf("login redirect limit exceeded")
		}
		if !verifier.scope.HostAllowed(request.URL.Hostname()) {
			return fmt.Errorf("scope: login redirect host out of authorized range")
		}
		if len(via) == 1 && request.Response != nil && isRedirectAwayFromLogin(request.Response) {
			outcome.RedirectAway = true
		}
		return nil
	}
}

func isRedirectAwayFromLogin(response *http.Response) bool {
	if response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest {
		return false
	}
	return !strings.Contains(strings.ToLower(response.Header.Get("Location")), "login")
}

func containsLoginText(body string, terms []string) bool {
	body = strings.ToLower(body)
	for _, term := range terms {
		if strings.Contains(body, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func loginSessionURL(spec FindingSpec, loginURL *url.URL) *url.URL {
	if strings.TrimSpace(spec.URL) == "" {
		return loginURL
	}
	payloadURL, err := parseVerificationURL(spec.URL)
	if err != nil {
		return loginURL
	}
	return payloadURL
}

func normalizedLoginContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "application/x-www-form-urlencoded"
	}
	return contentType
}

func loginCurlCommand(spec FindingSpec) string {
	credentialSpec := spec
	credentialSpec.Method = normalizedLoginMethod(spec.LoginMethod)
	credentialSpec.URL = spec.LoginURL
	credentialSpec.Body = redactedLoginBody(spec.LoginBody, spec.LoginContentType)
	credentialSpec.Headers = map[string]string{"Content-Type": normalizedLoginContentType(spec.LoginContentType)}
	return CurlCommand(credentialSpec)
}

func redactedLoginBody(body, contentType string) string {
	if !strings.HasPrefix(strings.ToLower(normalizedLoginContentType(contentType)), "application/x-www-form-urlencoded") {
		return "REDACTED"
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return "REDACTED"
	}
	for key := range values {
		if isSensitiveLoginField(key) {
			values[key] = []string{"REDACTED"}
		}
	}
	return values.Encode()
}

func isSensitiveLoginField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "password", "pass", "passwd", "pwd", "secret", "token":
		return true
	default:
		return false
	}
}

func cookieEvidence(cookies []*http.Cookie) ([]string, string) {
	if len(cookies) == 0 {
		return nil, ""
	}
	sorted := append([]*http.Cookie(nil), cookies...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	names := make([]string, 0, len(sorted))
	values := make([]string, 0, len(sorted))
	for _, cookie := range sorted {
		names = append(names, cookie.Name)
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	return names, strings.Join(values, "; ")
}

func parseVerificationURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("URL has no host")
	}
	return parsed, nil
}

func normalizedHTTPMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

func supportedVerificationMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

func isIdempotentMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func inconclusiveResult(spec FindingSpec, reason string) VerificationResult {
	return VerificationResult{
		Verdict:      VerdictInconclusive,
		VulnType:     spec.VulnType,
		ChecksFailed: []string{reason},
		Summary:      fmt.Sprintf("%s INCONCLUSIVE: %s", spec.VulnType, reason),
		Curl:         CurlCommand(spec),
	}
}

// CurlCommand renders a shell-safe reproduction command for report consumers.
func CurlCommand(spec FindingSpec) string {
	method := normalizedHTTPMethod(spec.Method)
	if !supportedVerificationMethod(method) {
		return ""
	}
	parts := []string{"curl", "-i", "-X", method}
	keys := make([]string, 0, len(spec.Headers))
	for key := range spec.Headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, "-H", shellQuote(key+": "+spec.Headers[key]))
	}
	if spec.Body != "" {
		parts = append(parts, "--data-raw", shellQuote(spec.Body))
	}
	parts = append(parts, shellQuote(spec.URL))
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
