package main

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

type minioObjectStore struct {
	client *minio.Client
	bucket string
}

func (s *minioObjectStore) put(ctx context.Context, key string, content []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *minioObjectStore) get(ctx context.Context, key string) ([]byte, objectInfo, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, objectInfo{}, normalizeMinIOError(err)
	}
	defer object.Close()
	stat, err := object.Stat()
	if err != nil {
		return nil, objectInfo{}, normalizeMinIOError(err)
	}
	content, err := io.ReadAll(io.LimitReader(object, stat.Size+1))
	if err != nil {
		return nil, objectInfo{}, err
	}
	if int64(len(content)) != stat.Size {
		return nil, objectInfo{}, fmt.Errorf("object size mismatch")
	}
	return content, objectInfo{Key: key, Size: stat.Size, ContentType: stat.ContentType, UpdatedAt: stat.LastModified}, nil
}

func (s *minioObjectStore) list(ctx context.Context, prefix string) ([]objectInfo, error) {
	result := make([]objectInfo, 0)
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		result = append(result, objectInfo{Key: object.Key, Size: object.Size, UpdatedAt: object.LastModified})
	}
	return result, nil
}

func (s *minioObjectStore) delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	return normalizeMinIOError(err)
}

func normalizeMinIOError(err error) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" {
		return errObjectNotFound
	}
	return err
}
