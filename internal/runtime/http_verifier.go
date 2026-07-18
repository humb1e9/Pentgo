package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const defaultVerificationBodyBytes = 64 * 1024

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

// VerificationRecord captures the framework-owned HTTP exchanges used to
// reach a verification verdict.
type VerificationRecord struct {
	Method               string            `json:"method"`
	PayloadURL           string            `json:"payload_url"`
	BaselineURL          string            `json:"baseline_url,omitempty"`
	RequestHeaders       map[string]string `json:"request_headers,omitempty"`
	RequestBody          string            `json:"request_body,omitempty"`
	BaselineRequestBody  string            `json:"baseline_request_body,omitempty"`
	PayloadStatus        int               `json:"payload_status,omitempty"`
	PayloadResponseBody  string            `json:"payload_response_body,omitempty"`
	PayloadLocation      string            `json:"payload_location,omitempty"`
	BaselineStatus       int               `json:"baseline_status,omitempty"`
	BaselineResponseBody string            `json:"baseline_response_body,omitempty"`
	Reproductions        int               `json:"reproductions,omitempty"`
	ScopeRejected        bool              `json:"scope_rejected,omitempty"`
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
		payload, err = verifier.request(ctx, method, payloadURL.String(), spec.Body, spec.Headers)
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
	})
	result.Curl = CurlCommand(spec)
	if !isIdempotentMethod(method) {
		result.ChecksFailed = append(result.ChecksFailed, "P1 replay limited to one non-idempotent request")
		result.Summary += "; non-idempotent request sent once"
	}
	return result, record
}

func newVerificationRecord(spec FindingSpec) VerificationRecord {
	return VerificationRecord{
		Method:      normalizedHTTPMethod(spec.Method),
		PayloadURL:  spec.URL,
		BaselineURL: spec.BaselineURL,
	}
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
