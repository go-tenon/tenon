package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

const (
	docDirectUploads = "docs/libraries/direct-uploads.md"
	docFilesystem    = "docs/libraries/filesystem.md"
)

// Doctor 返回检查 S3 磁盘的一组自检，组名为 s3：
//
//   - public_url：写入一个探测对象，从 public_url 下载它，确认文件可以公开访问。从本机连不上 public_url 时只提示：
//     它可能是只有浏览器才能访问的地址；
//   - cors：传入了 uploadOrigins 时检查 bucket 的 CORS 规则允许这些来源直传：PUT 方法、请求头 Content-Type
//     和 If-None-Match、暴露响应头 ETag。uploadOrigins 是调用直传的前端的来源，例如 https://example.com；
//     不用直传时不传。存储不提供 CORS 规则的接口时跳过；
//   - conditional_write：传入了 uploadOrigins 时确认存储支持条件写入（If-None-Match: *）。直传地址靠它保证
//     只能创建对象：不支持时，确认之后还在有效期内的上传地址可以替换已经校验过的文件。
//
// 读写权限由 filesystem.Doctor 检查。d 在检查时才使用。
func Doctor(d *Disk, uploadOrigins ...string) doctor.Group {
	checks := []doctor.Check{
		{Name: "public_url", Run: func(ctx context.Context) error { return checkPublicURL(ctx, d) }},
	}
	if len(uploadOrigins) > 0 {
		checks = append(checks, doctor.Check{Name: "cors", Run: func(ctx context.Context) error {
			out, err := d.client.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String(d.bucket)})
			if err != nil {
				return corsError(err)
			}
			return checkCORS(out.CORSRules, uploadOrigins)
		}}, doctor.Check{Name: "conditional_write", Run: func(ctx context.Context) error { return checkConditionalWrite(ctx, d) }})
	}
	return doctor.Group{Name: "s3", Checks: checks}
}

// checkPublicURL 写入探测对象，从公开地址下载并比较，最后删除它。
func checkPublicURL(ctx context.Context, d *Disk) (err error) {
	key := "tenon-doctor/" + strings.ToLower(rand.Text())
	body := []byte("tenon doctor probe")
	if err := d.Put(ctx, key, "text/plain", bytes.NewReader(body), int64(len(body))); err != nil {
		return err
	}
	defer func() {
		if delErr := d.Delete(context.WithoutCancel(ctx), key); delErr != nil && err == nil {
			err = delErr
		}
	}()
	url := d.URL(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doctor.Warn(
			"cannot reach public_url from this host: "+err.Error(),
			"if browsers reach it through another network, open "+d.baseURL+"/ in a browser to confirm;\n"+
				"otherwise set public_url to the bucket's public address or the CDN in front of it",
		).See(docFilesystem)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if err != nil {
		return err
	}
	return checkPublicResponse(resp.StatusCode, got, body, d.baseURL)
}

// checkConditionalWrite 用 If-None-Match: * 写两次同一个探测对象：第二次必须被拒绝（412）。
func checkConditionalWrite(ctx context.Context, d *Disk) (err error) {
	key := "tenon-doctor/" + strings.ToLower(rand.Text())
	put := func(body string) error {
		_, err := d.client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(d.bucket), Key: aws.String(key), Body: strings.NewReader(body),
			ContentLength: aws.Int64(int64(len(body))), IfNoneMatch: aws.String("*"),
		})
		return err
	}
	if err := put("first"); err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && slices.Contains([]string{"NotImplemented", "NotSupported", "InvalidArgument"}, apiErr.ErrorCode()) {
			return doctor.Fail("the storage rejects conditional writes (If-None-Match: *): "+apiErr.ErrorCode(), conditionalFix).See(docDirectUploads)
		}
		return wrap("put object", err)
	}
	defer func() {
		if delErr := d.Delete(context.WithoutCancel(ctx), key); delErr != nil && err == nil {
			err = delErr
		}
	}()
	err = put("second")
	var apiErr smithy.APIError
	switch {
	case err == nil:
		return doctor.Fail(
			"the storage ignores If-None-Match: * and overwrote an existing object, so an upload URL still in its "+
				"validity period can replace a file after it was confirmed",
			conditionalFix,
		).See(docDirectUploads)
	case errors.As(err, &apiErr) && (apiErr.ErrorCode() == "PreconditionFailed" || apiErr.ErrorCode() == "412"):
		return nil
	}
	return wrap("put object", err)
}

const conditionalFix = "use a storage that supports conditional writes (If-None-Match: *) on PutObject and\n" +
	"CompleteMultipartUpload, for example AWS S3, Cloudflare R2 or a recent MinIO; or do not use direct uploads"

// checkPublicResponse 判断从公开地址下载探测对象的结果。
func checkPublicResponse(status int, got, want []byte, baseURL string) error {
	switch {
	case status == http.StatusOK && bytes.Equal(got, want):
		return nil
	case status == http.StatusOK:
		return doctor.Fail(
			"public_url "+baseURL+" serves something else, not the objects of this bucket",
			"set public_url to the address that serves this bucket's objects, for example https://<bucket>.<endpoint> or the CDN",
		).See(docFilesystem)
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		return doctor.Fail(
			fmt.Sprintf("objects are not publicly readable at %s (HTTP %d)", baseURL, status),
			"allow anonymous GetObject on the bucket (a bucket policy), or put a CDN with access to the bucket in front of it",
		).See(docFilesystem)
	}
	return doctor.Fail(
		fmt.Sprintf("public_url %s does not serve the objects of this bucket (HTTP %d)", baseURL, status),
		"set public_url to the bucket's public address or the CDN in front of it; with path-style addressing it includes the bucket name",
	).See(docFilesystem)
}

// corsError 把读取 CORS 规则的错误转换为结论：没有规则时失败，存储不支持或没有权限读取时跳过。
func corsError(err error) error {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.ErrorCode() {
	case "NoSuchCORSConfiguration":
		return doctor.Fail("the bucket has no CORS rules, so browsers cannot upload to it directly", corsFix).See(docDirectUploads)
	case "NotImplemented", "NotSupported", "MethodNotAllowed", "XNotImplemented":
		return doctor.Skip("the storage does not report CORS rules; try a direct upload in a browser")
	case "AccessDenied":
		return doctor.Skip("the credentials cannot read the CORS rules (s3:GetBucketCORS); check them in the storage console")
	}
	return err
}

const corsFix = "add a CORS rule that allows the application's origin with method PUT,\n" +
	"allowed headers Content-Type and If-None-Match (or *) and exposed header ETag"

// checkCORS 检查 rules 中有允许每个来源直传的规则。
func checkCORS(rules []types.CORSRule, origins []string) error {
	var missing []string
	for _, origin := range origins {
		if !slices.ContainsFunc(rules, func(r types.CORSRule) bool { return allowsUpload(r, origin) }) {
			missing = append(missing, origin)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return doctor.Fail(
		"no CORS rule lets "+strings.Join(missing, ", ")+" upload directly (PUT with Content-Type and If-None-Match, exposing ETag)",
		corsFix,
	).See(docDirectUploads)
}

// allowsUpload 判断一条规则是否允许 origin 直传。
func allowsUpload(r types.CORSRule, origin string) bool {
	return slices.ContainsFunc(r.AllowedOrigins, func(p string) bool { return matchOrigin(p, origin) }) &&
		slices.ContainsFunc(r.AllowedMethods, func(m string) bool { return strings.EqualFold(m, http.MethodPut) }) &&
		allowsHeader(r, "content-type") && allowsHeader(r, "if-none-match") &&
		slices.ContainsFunc(r.ExposeHeaders, func(h string) bool { return strings.EqualFold(h, "etag") })
}

func allowsHeader(r types.CORSRule, name string) bool {
	return slices.ContainsFunc(r.AllowedHeaders, func(h string) bool { return h == "*" || strings.EqualFold(h, name) })
}

// matchOrigin 按 S3 的规则匹配来源：pattern 中最多一个 *，匹配任意字符。
func matchOrigin(pattern, origin string) bool {
	before, after, wildcard := strings.Cut(pattern, "*")
	if !wildcard {
		return strings.EqualFold(pattern, origin)
	}
	o := strings.ToLower(origin)
	before, after = strings.ToLower(before), strings.ToLower(after)
	return len(o) >= len(before)+len(after) && strings.HasPrefix(o, before) && strings.HasSuffix(o, after)
}
