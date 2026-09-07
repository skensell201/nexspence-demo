package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/netguard"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/safego"
)

// WebhookService handles CRUD for webhooks and fires delivery goroutines.
type WebhookService struct {
	repo   repository.WebhookRepo
	client *http.Client
	log    logger.Logger
}

// NewWebhookService constructs a service for managing and delivering webhooks.
// The delivery client is SSRF-guarded: it refuses to dial internal addresses,
// since webhook URLs are user-configured.
func NewWebhookService(repo repository.WebhookRepo) *WebhookService {
	return &WebhookService{
		repo:   repo,
		client: netguard.Client(10 * time.Second),
	}
}

// WithLogger sets the logger used to recover and report a panic in a detached webhook delivery.
func (s *WebhookService) WithLogger(log logger.Logger) *WebhookService {
	s.log = log
	return s
}

// WithHTTPClient overrides the delivery HTTP client. Intended for tests that
// need to reach loopback test servers the SSRF guard would otherwise block.
func (s *WebhookService) WithHTTPClient(c *http.Client) *WebhookService {
	s.client = c
	return s
}

// List returns all configured webhooks (never nil).
func (s *WebhookService) List(ctx context.Context) ([]domain.Webhook, error) {
	wh, err := s.repo.List(ctx)
	if wh == nil {
		wh = []domain.Webhook{}
	}
	return wh, err
}

// Get returns the webhook with the given id.
func (s *WebhookService) Get(ctx context.Context, id string) (*domain.Webhook, error) {
	return s.repo.Get(ctx, id)
}

// Create validates the webhook (name, URL, at least one event) and persists it as active.
func (s *WebhookService) Create(ctx context.Context, w *domain.Webhook) error {
	if w.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if w.URL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidInput)
	}
	if len(w.Events) == 0 {
		return fmt.Errorf("%w: at least one event is required", ErrInvalidInput)
	}
	w.Active = true
	return s.repo.Create(ctx, w)
}

// Update validates and persists changes to an existing webhook.
func (s *WebhookService) Update(ctx context.Context, w *domain.Webhook) error {
	if w.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if w.URL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidInput)
	}
	if len(w.Events) == 0 {
		return fmt.Errorf("%w: at least one event is required", ErrInvalidInput)
	}
	return s.repo.Update(ctx, w)
}

// Delete removes the webhook with the given id.
func (s *WebhookService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// TestResult holds the outcome of a synchronous test delivery.
type TestResult struct {
	Status    int   `json:"status"`
	LatencyMs int64 `json:"latency_ms"`
}

// Test sends a ping payload to the webhook identified by id and returns the
// HTTP status + round-trip latency. Returns an error if the webhook is not
// found or the HTTP request cannot be made.
func (s *WebhookService) Test(ctx context.Context, id string) (*TestResult, error) {
	wh, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if wh == nil {
		return nil, fmt.Errorf("webhook %q not found", id)
	}
	payload := domain.WebhookPayload{
		Event:      "webhook.test",
		Timestamp:  time.Now().UTC(),
		Repository: "test",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	status, err := s.deliverWithStatus(*wh, body, "webhook.test")
	if err != nil {
		return nil, err
	}
	return &TestResult{Status: status, LatencyMs: time.Since(start).Milliseconds()}, nil
}

// Dispatch fires the payload to all active webhooks subscribed to payload.Event.
// Delivery is asynchronous — errors are silently dropped.
func (s *WebhookService) Dispatch(payload domain.WebhookPayload) {
	safego.Go(s.log, "webhook-dispatch", func() {
		hooks, err := s.repo.ListByEvent(context.Background(), payload.Event)
		if err != nil || len(hooks) == 0 {
			return
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		for _, wh := range hooks {
			if !wh.Active {
				continue
			}
			s.deliver(wh, body, payload.Event)
		}
	})
}

func (s *WebhookService) deliver(wh domain.Webhook, body []byte, event domain.WebhookEvent) {
	_, _ = s.deliverWithStatus(wh, body, string(event))
}

func (s *WebhookService) deliverWithStatus(wh domain.Webhook, body []byte, event string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nexspence-Event", event)
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(body)
		req.Header.Set("X-Nexspence-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
