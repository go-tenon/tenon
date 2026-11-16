package filesystem

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
)

// doctorPrefix 是自检写入的探测对象的键前缀。
const doctorPrefix = "tenon-doctor/"

// Doctor 返回检查磁盘的一组自检，组名为 storage：read_write 写入一个很小的探测对象，读回比较内容，再删除它，
// 确认程序对磁盘有写、读、删的权限。本地磁盘对应目录可写；S3 对应 bucket、地址和凭据都正确。
// disk 在检查时才使用。
func Doctor(disk Disk) doctor.Group {
	return doctor.Group{Name: "storage", Checks: []doctor.Check{
		{Name: "read_write", Run: func(ctx context.Context) error { return probe(ctx, disk) }},
	}}
}

const storageFix = "local disk: make the directory writable by the user running the program\n" +
	"(in Docker, mount a volume owned by that user);\n" +
	"S3: check the bucket, endpoint and credentials, which need PutObject, GetObject and DeleteObject"

// probe 写入、读回并删除一个探测对象。
func probe(ctx context.Context, disk Disk) error {
	key := doctorPrefix + strings.ToLower(rand.Text())
	body := []byte("tenon doctor probe")
	if err := disk.Put(ctx, key, "text/plain", bytes.NewReader(body), int64(len(body))); err != nil {
		return doctor.Fail("cannot write to the storage: "+err.Error(), storageFix)
	}
	read := func() error {
		r, err := disk.Open(ctx, key)
		if err != nil {
			return err
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, body) {
			return doctor.Fail("the storage returned different content than was written", storageFix)
		}
		return nil
	}
	readErr := read()
	if err := disk.Delete(ctx, key); err != nil {
		return doctor.Fail("cannot delete from the storage: "+err.Error(), storageFix)
	}
	if readErr != nil {
		if problem := (*doctor.Problem)(nil); errors.As(readErr, &problem) {
			return readErr
		}
		return doctor.Fail("cannot read from the storage: "+readErr.Error(), storageFix)
	}
	return nil
}
