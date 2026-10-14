// Package storage stores attendance photos in S3-compatible object storage
// (MinIO locally, Cloudflare R2 / S3 in production). Buckets stay private:
// photos are only served through the authenticated API.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, string, error)
	Delete(ctx context.Context, key string) error
	Ping(ctx context.Context) error
	URI(key string) string
}

type S3 struct {
	client *minio.Client
	bucket string
}

// urlStyle: "virtual" puts the bucket in the host name (bucket.endpoint, required by
// Railway Buckets), "path" in the path (MinIO, s3mock); "auto" lets the client pick.
func NewS3(endpoint, accessKey, secretKey, bucket, region string, useSSL bool, urlStyle string) (*S3, error) {
	lookup := map[string]minio.BucketLookupType{"path": minio.BucketLookupPath, "virtual": minio.BucketLookupDNS}[urlStyle] // else auto
	c, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       useSSL,
		Region:       region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: c, bucket: bucket}, nil
}

// EnsureBucket creates the bucket if missing (MinIO). On R2/S3 create it beforehand.
func (s *S3) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
	}
	return nil
}

func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, "", err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, "", err
	}
	return obj, info.Size, info.ContentType, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3) Ping(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucket)
	return err
}

func (s *S3) URI(key string) string { return fmt.Sprintf("s3://%s/%s", s.bucket, key) }
