package artifacts

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/signer"
	"github.com/sirupsen/logrus"

	"picotera/pkg/configx"
)

type Sink interface {
	Put(ctx context.Context, key string, payload []byte)
	PresignedGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Enabled() bool
	Close(ctx context.Context) error
}

// bucketLookup maps the tri-state PICOTERA_S3_PATH_STYLE setting to a minio
// BucketLookupType: nil = auto-detect, true = force path style, false = force
// virtual-hosted (DNS) style.
func bucketLookup(pathStyle *bool) minio.BucketLookupType {
	if pathStyle == nil {
		return minio.BucketLookupAuto
	}
	if *pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupDNS
}

func NewSink(cfg configx.S3Config, logger *logrus.Entry) (Sink, error) {
	if cfg.Endpoint == "" {
		logger.Info("artifact disabled (PICOTERA_S3_ENDPOINT empty)")
		return noopSink{}, nil
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.Bucket == "" {
		return nil, fmt.Errorf("artifact: s3 access_key, secret_key, bucket must be set when endpoint is configured")
	}
	lookup := bucketLookup(cfg.PathStyle)
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("artifact: create minio client: %w", err)
	}
	signerEndpoint := cfg.Endpoint
	signerSecure := cfg.UseSSL
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil {
			return nil, fmt.Errorf("artifact: parse public url: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("artifact: public url must include scheme and host")
		}
		signerEndpoint = u.Host
		signerSecure = u.Scheme == "https"
	}
	urlSignerClient, err := minio.New(signerEndpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       signerSecure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("artifact: create minio url signer client: %w", err)
	}
	s := &minioSink{
		client:          client,
		urlSignerClient: urlSignerClient,
		bucket:          cfg.Bucket,
		accessKey:       cfg.AccessKey,
		secretKey:       cfg.SecretKey,
		region:          cfg.Region,
		publicURL:       cfg.PublicURL,
		pathStyle:       cfg.PathStyle,
		logger:          logger,
		jobs:            make(chan job, 256),
	}
	for i := 0; i < 4; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	logger.WithField("bucket", cfg.Bucket).WithField("endpoint", cfg.Endpoint).Info("artifact sink ready")
	return s, nil
}

type noopSink struct{}

func (noopSink) Put(ctx context.Context, key string, payload []byte) {}
func (noopSink) PresignedGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return "", nil
}
func (noopSink) Enabled() bool { return false }
func (noopSink) Close(ctx context.Context) error {
	return nil
}

type job struct {
	key     string
	payload []byte
}

type minioSink struct {
	client          *minio.Client
	urlSignerClient *minio.Client
	bucket          string
	accessKey       string
	secretKey       string
	region          string
	publicURL       string
	pathStyle       *bool
	logger          *logrus.Entry
	jobs            chan job
	closeOnce       sync.Once
	mu              sync.Mutex
	closed          bool
	wg              sync.WaitGroup
}

func (s *minioSink) Enabled() bool { return true }

func (s *minioSink) Put(ctx context.Context, key string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.logger.WithField("key", key).Warn("artifact: sink closed, dropping")
		return
	}
	select {
	case s.jobs <- job{key: key, payload: payload}:
	case <-ctx.Done():
		s.logger.WithError(ctx.Err()).WithField("key", key).Warn("artifact: context cancelled, dropping")
	default:
		s.logger.WithField("key", key).Warn("artifact: queue full, dropping")
	}
}

func (s *minioSink) worker() {
	defer s.wg.Done()
	for j := range s.jobs {
		s.upload(j)
	}
}

func (s *minioSink) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.jobs)
		s.mu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *minioSink) upload(j job) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.bucket, j.key, bytes.NewReader(j.payload), int64(len(j.payload)), minio.PutObjectOptions{
		ContentType:     "application/json",
		ContentEncoding: "zstd",
	})
	if err != nil {
		s.logger.WithError(err).WithField("key", j.key).Warn("artifact: upload failed")
	}
}

func (s *minioSink) PresignedGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.publicURL != "" && s.pathStyle != nil && !*s.pathStyle {
		return s.presignedGetPublicVirtualHosted(key, ttl)
	}
	u, err := s.urlSignerClient.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", err
	}
	if s.publicURL == "" {
		return u.String(), nil
	}
	pub, err := url.Parse(s.publicURL)
	if err != nil {
		return "", fmt.Errorf("artifact: parse public url: %w", err)
	}
	u.Scheme = pub.Scheme
	u.Host = pub.Host
	if pub.Path != "" && pub.Path != "/" {
		u.Path = pub.Path + u.Path
	}
	return u.String(), nil
}

func (s *minioSink) presignedGetPublicVirtualHosted(key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("artifact: presign ttl must be positive")
	}
	pub, err := url.Parse(s.publicURL)
	if err != nil {
		return "", fmt.Errorf("artifact: parse public url: %w", err)
	}
	if pub.Scheme == "" || pub.Host == "" {
		return "", fmt.Errorf("artifact: public url must include scheme and host")
	}
	pub.RawQuery = ""
	pub.Fragment = ""
	pub.Path = joinURLPath(pub.Path, key)
	pub.RawPath = ""

	req, err := http.NewRequest(http.MethodGet, pub.String(), nil)
	if err != nil {
		return "", fmt.Errorf("artifact: create presign request: %w", err)
	}
	req.Header.Set("Host", strings.ToLower(pub.Host))
	req = signer.PreSignV4(*req, s.accessKey, s.secretKey, "", s.region, int64(ttl/time.Second))
	return req.URL.String(), nil
}

func joinURLPath(prefix, key string) string {
	if prefix == "" || prefix == "/" {
		return "/" + strings.TrimPrefix(key, "/")
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimPrefix(key, "/")
}
