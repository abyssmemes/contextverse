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

	"github.com/orkcom-tech/contextverse/internal/logx"
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

func (s *S3) headKey(scope string) string {
	sc := sanitizePath(scope)
	if sc == "" || sc == "." {
		sc = "_root"
	}
	sum := contentVersion([]byte(sc))
	return s.prefix + "heads/" + string(sum) + ".head"
}

// getRecord reads a path, looking under the legacy key when the current one is
// absent so a bucket written by an older contextd keeps working.
//
// Returns the key it found the object under, because a writer needs to know
// whether it is replacing a legacy object and should clean it up.
func (s *S3) getRecord(ctx context.Context, path string) (s3ObjectRecord, string, string, error) {
	key := s.objectKey(path)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil && isNoSuchKey(err) {
		key = s.legacyObjectKey(path)
		out, err = s.client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
	}
	if err != nil {
		if isNoSuchKey(err) {
			return s3ObjectRecord{}, "", "", ErrNotFound
		}
		return s3ObjectRecord{}, "", "", err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(out.Body)
	if err != nil {
		return s3ObjectRecord{}, "", "", err
	}
	var rec s3ObjectRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return s3ObjectRecord{}, "", "", err
	}
	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}
	return rec, etag, key, nil
}

func (s *S3) Get(ctx context.Context, path string) ([]byte, Version, error) {
	path, err := CleanFilePath(path)
	if err != nil {
		return nil, "", err
	}
	rec, _, _, err := s.getRecord(ctx, path)
	if err != nil {
		return nil, "", err
	}
	return append([]byte(nil), rec.Data...), rec.Version, nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Entry, error) {
	prefix, err := CleanPath(prefix)
	if err != nil {
		return nil, err
	}
	// The listing is the answer for anything written under the current scheme:
	// the key carries the path, so no object needs to be fetched. Only legacy
	// keys — which say nothing about their path — still cost a GET, and each one
	// stops costing it the next time that file is written.
	//
	// This used to issue a GetObject for every object in the bucket and download
	// the whole body to read the path back out of it, on every tree, every
	// changes and every quota check.
	var out []Entry
	var current []listedObject
	var legacyKeys []string
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix + s3ObjectsPrefix),
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
			path, ok := s.pathFromKey(*obj.Key)
			if !ok {
				legacyKeys = append(legacyKeys, *obj.Key)
				continue
			}
			if prefix != "" && !strings.HasPrefix(path, prefix) {
				continue
			}
			current = append(current, listedObject{path: path, key: *obj.Key})
		}
	}

	// One HeadObject each: metadata, no body. The version is a few bytes of
	// header rather than the whole file, which is what this used to move.
	for _, obj := range current {
		ver, err := s.versionOf(ctx, obj.key)
		if err != nil {
			logx.L().Warn("s3 list: skipping unreadable object", "key", obj.key, "err", err)
			continue
		}
		out = append(out, Entry{Path: obj.path, Version: ver})
	}

	for _, key := range legacyKeys {
		rec, err := s.readRecordByKey(ctx, key)
		if err != nil {
			// One unreadable legacy object must not cost the whole listing.
			logx.L().Warn("s3 list: skipping unreadable legacy object", "key", key, "err", err)
			continue
		}
		if prefix != "" && !strings.HasPrefix(rec.Path, prefix) {
			continue
		}
		out = append(out, Entry{Path: rec.Path, Version: rec.Version})
	}
	return out, nil
}

// s3VersionMeta is the user-metadata key holding the CAS token.
const s3VersionMeta = "cv-version"

// listedObject is one key a listing recognised as belonging to a path.
type listedObject struct {
	path string
	key  string
}

// versionOf reads an object's CAS token from its metadata, falling back to the
// body for an object written before the stamp existed.
func (s *S3) versionOf(ctx context.Context, key string) (Version, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	for k, v := range head.Metadata {
		// S3 lowercases metadata keys, and SDKs differ on whether they hand
		// them back canonicalised.
		if strings.EqualFold(k, s3VersionMeta) && v != "" {
			return Version(v), nil
		}
	}
	rec, err := s.readRecordByKey(ctx, key)
	if err != nil {
		return "", err
	}
	return rec.Version, nil
}

// readRecordByKey fetches one object by its exact key, for the legacy objects a
// listing cannot describe on its own.
func (s *S3) readRecordByKey(ctx context.Context, key string) (s3ObjectRecord, error) {
	got, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return s3ObjectRecord{}, err
	}
	defer got.Body.Close()
	raw, err := io.ReadAll(got.Body)
	if err != nil {
		return s3ObjectRecord{}, err
	}
	var rec s3ObjectRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return s3ObjectRecord{}, err
	}
	return rec, nil
}

func (s *S3) Put(ctx context.Context, path string, data []byte, expected Version) (Version, error) {
	path, err := CleanFilePath(path)
	if err != nil {
		return "", err
	}
	if err := s.checkKeyLength(path); err != nil {
		return "", err
	}
	rec, etag, foundKey, err := s.getRecord(ctx, path)
	actual := Version("")
	if err == nil {
		actual = rec.Version
	} else if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if actual != expected {
		return "", &ConflictError{Path: path, Expected: expected, Actual: actual}
	}
	// A legacy object is being replaced, so the write goes to the new key and
	// the old one is dropped afterwards. Migration happens as a bucket is used
	// rather than in a step somebody has to remember to run.
	migrating := foundKey != "" && foundKey != s.objectKey(path)
	if migrating {
		etag = "" // the precondition belongs to the key we are writing, not the one we read
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
		// The CAS token, stamped where a HeadObject can read it. Listing needs
		// the version as well as the path, and the alternative is downloading
		// every file to find out what version it is.
		Metadata: map[string]string{s3VersionMeta: string(next)},
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
	if migrating {
		// Best-effort: the object is already safe under its new key, and a
		// leftover legacy copy is only read when the new one is missing.
		if _, derr := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(foundKey),
		}); derr != nil {
			logx.L().Warn("s3 migrate: could not remove the legacy object", "path", path, "key", foundKey, "err", derr)
		}
	}
	logx.L().Debug("s3 put", "path", path, "version", string(next))
	return next, nil
}

func (s *S3) Delete(ctx context.Context, path string, expected Version) error {
	path, err := CleanFilePath(path)
	if err != nil {
		return err
	}
	rec, _, foundKey, err := s.getRecord(ctx, path)
	if err != nil {
		return err
	}
	if rec.Version != expected {
		return &ConflictError{Path: path, Expected: expected, Actual: rec.Version}
	}
	// Delete the key it was actually found under: a legacy object deleted at
	// the new key would stay readable.
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(foundKey),
	})
	return err
}

func (s *S3) Head(ctx context.Context, scope string) (Version, error) {
	scope, err := CleanPath(scope)
	if err != nil {
		return "", err
	}
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
	scope, err := CleanPath(scope)
	if err != nil {
		return err
	}
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
