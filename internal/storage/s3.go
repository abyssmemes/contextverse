package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/abyssmemes/contextverse/internal/logx"
)

// S3Config configures an S3 / MinIO backend.
type S3Config struct {
	Endpoint  string // e.g. http://127.0.0.1:9000 (empty = AWS)
	Region    string
	Bucket    string
	Prefix    string // key prefix, no leading slash
	AccessKey string
	SecretKey string
	PathStyle bool
}

type s3ObjectRecord struct {
	Path    string  `json:"path"`
	Version Version `json:"version"`
	Data    []byte  `json:"data"`
}

// S3 is an S3-compatible blob store with optimistic CAS via get+conditional put.
type S3 struct {
	client *s3.Client
	bucket string
	prefix string
}

// OpenS3 creates an S3 backend client.
func OpenS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("%w: s3 bucket required", ErrInvalidArgument)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}

	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(cfg.Region))
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle || cfg.Endpoint != ""
	})

	s := &S3{client: client, bucket: cfg.Bucket, prefix: prefix}
	// Ensure bucket exists (MinIO / fresh envs). Ignore if already owned.
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)})
	if err != nil {
		_, cerr := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)})
		if cerr != nil && !isBucketAlreadyOwned(cerr) {
			return nil, fmt.Errorf("ensure bucket %s: head=%v create=%w", cfg.Bucket, err, cerr)
		}
	}
	return s, nil
}

func isBucketAlreadyOwned(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return true
		}
	}
	return false
}

func (s *S3) Name() string { return "s3" }

func (s *S3) objectKey(path string) string {
	sum := contentVersion([]byte(sanitizePath(path)))
	return s.prefix + "objects/" + string(sum) + ".json"
}

func (s *S3) headKey(scope string) string {
	sc := sanitizePath(scope)
	if sc == "" || sc == "." {
		sc = "_root"
	}
	sum := contentVersion([]byte(sc))
	return s.prefix + "heads/" + string(sum) + ".head"
}

func (s *S3) getRecord(ctx context.Context, path string) (s3ObjectRecord, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(path)),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return s3ObjectRecord{}, "", ErrNotFound
		}
		return s3ObjectRecord{}, "", err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(out.Body)
	if err != nil {
		return s3ObjectRecord{}, "", err
	}
	var rec s3ObjectRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return s3ObjectRecord{}, "", err
	}
	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}
	return rec, etag, nil
}

func (s *S3) Get(ctx context.Context, path string) ([]byte, Version, error) {
	rec, _, err := s.getRecord(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return append([]byte(nil), rec.Data...), rec.Version, nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Entry, error) {
	var out []Entry
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix + "objects/"),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			got, err := s.client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    obj.Key,
			})
			if err != nil {
				return nil, err
			}
			raw, err := io.ReadAll(got.Body)
			got.Body.Close()
			if err != nil {
				return nil, err
			}
			var rec s3ObjectRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}
			if prefix != "" && !strings.HasPrefix(rec.Path, prefix) {
				continue
			}
			out = append(out, Entry{Path: rec.Path, Version: rec.Version})
		}
	}
	return out, nil
}

func (s *S3) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	rec, etag, err := s.getRecord(ctx, path)
	actual := Version("")
	if err == nil {
		actual = rec.Version
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if actual != expected {
		return "", &ConflictError{Path: path, Expected: expected, Actual: actual}
	}
	next := contentVersion(data)
	nrec := s3ObjectRecord{Path: sanitizePath(path), Version: next, Data: append([]byte(nil), data...)}
	raw, err := json.Marshal(nrec)
	if err != nil {
		return "", err
	}
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.objectKey(path)),
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	}
	if etag != "" {
		input.IfMatch = aws.String(etag)
	} else {
		input.IfNoneMatch = aws.String("*")
	}
	_, err = s.client.PutObject(ctx, input)
	if err != nil {
		if isPreconditionFailed(err) {
			return "", &ConflictError{Path: path, Expected: expected, Actual: "concurrent"}
		}
		return "", err
	}
	logx.L().Debug("s3 put", "path", path, "version", string(next))
	return next, nil
}

func (s *S3) Delete(ctx context.Context, path string, expected Version) error {
	rec, _, err := s.getRecord(ctx, path)
	if err != nil {
		return err
	}
	if rec.Version != expected {
		return &ConflictError{Path: path, Expected: expected, Actual: rec.Version}
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(path)),
	})
	return err
}

func (s *S3) Head(ctx context.Context, scope string) (Version, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.headKey(scope)),
	})
	if err != nil {
		if isNoSuchKey(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(out.Body)
	if err != nil {
		return "", err
	}
	return Version(strings.TrimSpace(string(raw))), nil
}

func (s *S3) SetHead(ctx context.Context, scope string, expected, next Version) error {
	key := s.headKey(scope)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	actual := Version("")
	etag := ""
	if err == nil {
		raw, rerr := io.ReadAll(out.Body)
		out.Body.Close()
		if rerr != nil {
			return rerr
		}
		actual = Version(strings.TrimSpace(string(raw)))
		if out.ETag != nil {
			etag = strings.Trim(*out.ETag, `"`)
		}
	} else if !isNoSuchKey(err) {
		return err
	}
	if actual != expected {
		return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: actual}
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(string(next) + "\n")),
	}
	if etag != "" {
		input.IfMatch = aws.String(etag)
	} else {
		input.IfNoneMatch = aws.String("*")
	}
	_, err = s.client.PutObject(ctx, input)
	if err != nil {
		if isPreconditionFailed(err) {
			return &ConflictError{Path: "head:" + scope, Expected: expected, Actual: "concurrent"}
		}
		return err
	}
	return nil
}

// TestConnectivity lists the bucket prefix.
func (s *S3) TestConnectivity(ctx context.Context) error {
	_, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  aws.String(s.prefix),
		MaxKeys: aws.Int32(1),
	})
	return err
}

func isNoSuchKey(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}

func isPreconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "412":
			return true
		}
	}
	return false
}
