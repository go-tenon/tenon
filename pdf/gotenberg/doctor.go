package gotenberg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/doctor"
	"github.com/go-tenon/tenon/pdf"
)

const docPDF = "docs/libraries/pdf.md"

// Doctor 返回检查 Gotenberg 的一组自检，组名为 gotenberg：
//
//   - health：/health 报告 Gotenberg 和其中的 Chromium 都在运行；
//   - render：生成一页很小的 PDF，确认地址、用户名和密码都正确，Chromium 能完成渲染。
//
// r 在检查时才使用。
func Doctor(r *Renderer) doctor.Group {
	return doctor.Group{Name: "gotenberg", Checks: []doctor.Check{
		{Name: "health", Run: r.checkHealth},
		{Name: "render", Run: func(ctx context.Context) error {
			data, err := r.Render(ctx, pdf.Document{HTML: []byte("<p>tenon doctor</p>")})
			if err != nil {
				return renderProblem(err)
			}
			if !bytes.HasPrefix(data, []byte("%PDF")) {
				return doctor.Fail("Gotenberg returned something that is not a PDF", "check that url points to Gotenberg").See(docPDF)
			}
			return nil
		}},
	}}
}

// health 是 /health 的响应。
type health struct {
	Status  string `json:"status"`
	Details map[string]struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"details"`
}

func (r *Renderer) checkHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.cfg.URL, "/")+"/health", nil)
	if err != nil {
		return err
	}
	if r.cfg.Username != "" || r.cfg.Password != "" {
		req.SetBasicAuth(r.cfg.Username, r.cfg.Password)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return doctor.Fail("cannot reach Gotenberg: "+err.Error(),
			"check url and that this host may reach it, for example http://gotenberg:3000").See(docPDF)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return err
	}
	return checkHealth(resp.StatusCode, body)
}

// checkHealth 判断 /health 的响应：200 并且每个模块都是 up。Chromium 崩溃时 Gotenberg 返回 503。
func checkHealth(status int, body []byte) error {
	if status == http.StatusUnauthorized {
		return unauthorized()
	}
	var h health
	if err := json.Unmarshal(body, &h); err != nil {
		return doctor.Fail(fmt.Sprintf("/health did not answer like Gotenberg (HTTP %d)", status),
			"check that url points to Gotenberg, for example http://gotenberg:3000").See(docPDF)
	}
	var down []string
	for name, d := range h.Details {
		if d.Status != "up" {
			down = append(down, name+": "+d.Status+" "+d.Error)
		}
	}
	slices.Sort(down)
	if status == http.StatusOK && h.Status == "up" && len(down) == 0 {
		return nil
	}
	return doctor.Fail("Gotenberg is "+h.Status+" "+strings.Join(down, "; "),
		"restart Gotenberg and check its logs; Chromium needs enough memory (at least 1 GB)").See(docPDF)
}

func unauthorized() error {
	return doctor.Fail("Gotenberg rejected the username or password",
		"set username and password to the values of --api-basic-auth-username and --api-basic-auth-password").See(docPDF)
}

// renderProblem 把生成失败转换为结论。连不上或 Gotenberg 不可用时跳过：health 已经报告了原因。
func renderProblem(err error) error {
	if errors.Is(err, pdf.ErrUnavailable) {
		return doctor.Skip("Gotenberg is unavailable, see the health check")
	}
	if strings.Contains(err.Error(), "401") {
		return unauthorized()
	}
	return doctor.Fail("Gotenberg could not render a page: "+err.Error(),
		"check the Gotenberg logs; the Chromium module must be enabled").See(docPDF)
}
