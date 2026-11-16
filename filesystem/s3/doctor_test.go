package s3_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/go-tenon/tenon/doctor"
	s3disk "github.com/go-tenon/tenon/filesystem/s3"
)

// fakeDisk 连接内存 S3 服务，publicURL 为空时用服务自己的 path-style 地址作为公开地址：内存服务不检查权限，对象可以直接下载。
func fakeDisk(t *testing.T, publicURL func(server string) string) *s3disk.Disk {
	t.Helper()
	server := httptest.NewServer(gofakes3.New(s3mem.New()).Server())
	t.Cleanup(server.Close)
	client := awss3.New(awss3.Options{
		BaseEndpoint: aws.String(server.URL), Region: "us-east-1", UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider("k", "s", ""), RetryMaxAttempts: 1,
	})
	if _, err := client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String("test")}); err != nil {
		t.Fatal(err)
	}
	disk, err := s3disk.New(client, "test", publicURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

func TestDoctorPublicURL(t *testing.T) {
	t.Parallel()
	run := func(disk *s3disk.Disk) doctor.Result {
		t.Helper()
		return doctor.Run(context.Background(), s3disk.Doctor(disk)).Results[0]
	}
	if res := run(fakeDisk(t, func(s string) string { return s + "/test" })); res.Status != doctor.Passed {
		t.Errorf("public bucket: %+v", res)
	}

	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(forbidden.Close)
	if res := run(fakeDisk(t, func(string) string { return forbidden.URL })); res.Status != doctor.Failed ||
		!strings.Contains(res.Summary, "not publicly readable") {
		t.Errorf("private bucket: %+v", res)
	}

	if res := run(fakeDisk(t, func(string) string { return "http://127.0.0.1:1/files" })); res.Status != doctor.Warning {
		t.Errorf("unreachable public_url: %+v", res)
	}
}

func TestDoctorCORSOnlyWithOrigins(t *testing.T) {
	t.Parallel()
	disk := fakeDisk(t, func(s string) string { return s + "/test" })
	for _, res := range doctor.Run(context.Background(), s3disk.Doctor(disk)).Results {
		if res.Check == "cors" {
			t.Errorf("cors checked without upload origins: %+v", res)
		}
	}
}
