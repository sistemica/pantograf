package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3Driver speaks the S3 protocol via minio-go. Works against AWS S3,
// MinIO, Cloudflare R2, Backblaze B2, Wasabi — anything that implements
// the S3 API.
type s3Driver struct {
	cli    *minio.Client
	bucket string
}

func newS3Driver(endpoint, region, accessKey, secretKey, bucket string, useSSL bool) (*s3Driver, error) {
	if bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	host, secure, err := parseS3Endpoint(endpoint, useSSL)
	if err != nil {
		return nil, err
	}
	cli, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: client: %w", err)
	}
	return &s3Driver{cli: cli, bucket: bucket}, nil
}

// parseS3Endpoint accepts either a bare host:port or a full http(s)://...
// URL. minio-go expects a host-only string plus a `Secure` flag.
func parseS3Endpoint(raw string, useSSL bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// Default = AWS public.
		return "s3.amazonaws.com", true, nil
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false, fmt.Errorf("endpoint: %w", err)
		}
		host := u.Host
		secure := u.Scheme == "https"
		return host, secure, nil
	}
	return raw, useSSL, nil
}

func (d *s3Driver) List(ctx context.Context, prefix string, recursive bool, limit int) ([]Entry, error) {
	// minio-go's "recursive=false" mode uses a delimiter; that gives us
	// the "common prefixes" (i.e. subdirectories) as well as files.
	opts := minio.ListObjectsOptions{
		Prefix:    strings.TrimPrefix(prefix, "/"),
		Recursive: recursive,
	}
	if limit > 0 {
		opts.MaxKeys = limit
	}
	var out []Entry
	for obj := range d.cli.ListObjects(ctx, d.bucket, opts) {
		if obj.Err != nil {
			return out, obj.Err
		}
		entry := Entry{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified.UTC(),
			ContentType:  obj.ContentType,
			ETag:         obj.ETag,
		}
		// minio-go reports a "common prefix" as an Object with no size
		// and a key ending in /. Surface it as a directory.
		if strings.HasSuffix(obj.Key, "/") && obj.Size == 0 {
			entry.IsDir = true
		}
		out = append(out, entry)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *s3Driver) Stat(ctx context.Context, key string) (Entry, error) {
	key = strings.TrimPrefix(key, "/")
	info, err := d.cli.StatObject(ctx, d.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Key:          info.Key,
		Size:         info.Size,
		LastModified: info.LastModified.UTC(),
		ContentType:  info.ContentType,
		ETag:         info.ETag,
	}, nil
}

func (d *s3Driver) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	key = strings.TrimPrefix(key, "/")
	obj, err := d.cli.GetObject(ctx, d.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// minio-go returns a *minio.Object that doesn't actually error until
	// the first Read. Probe Stat() so the caller sees errors early.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

func (d *s3Driver) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Entry, error) {
	key = strings.TrimPrefix(key, "/")
	opts := minio.PutObjectOptions{ContentType: contentType}
	info, err := d.cli.PutObject(ctx, d.bucket, key, r, size, opts)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Key:          info.Key,
		Size:         info.Size,
		LastModified: time.Now().UTC(),
		ETag:         info.ETag,
		ContentType:  contentType,
	}, nil
}

func (d *s3Driver) Delete(ctx context.Context, key string) error {
	key = strings.TrimPrefix(key, "/")
	return d.cli.RemoveObject(ctx, d.bucket, key, minio.RemoveObjectOptions{})
}

// Presign issues a temporary URL for GET (or PUT) without exposing the
// credentials. method ∈ {GET, PUT, HEAD, DELETE}. expiry is clamped to
// AWS's 7-day max.
func (d *s3Driver) Presign(ctx context.Context, key string, expiry time.Duration, method string) (string, error) {
	key = strings.TrimPrefix(key, "/")
	if expiry <= 0 {
		expiry = 1 * time.Hour
	}
	if expiry > 7*24*time.Hour {
		expiry = 7 * 24 * time.Hour
	}
	switch strings.ToUpper(method) {
	case "", stdhttp.MethodGet:
		u, err := d.cli.PresignedGetObject(ctx, d.bucket, key, expiry, url.Values{})
		if err != nil {
			return "", err
		}
		return u.String(), nil
	case stdhttp.MethodPut:
		u, err := d.cli.PresignedPutObject(ctx, d.bucket, key, expiry)
		if err != nil {
			return "", err
		}
		return u.String(), nil
	case stdhttp.MethodHead:
		u, err := d.cli.PresignedHeadObject(ctx, d.bucket, key, expiry, url.Values{})
		if err != nil {
			return "", err
		}
		return u.String(), nil
	default:
		return "", fmt.Errorf("presign: unsupported method %q", method)
	}
}
