package clientgen

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/go-tenon/tenon/command"
	"github.com/go-tenon/tenon/httpx/rest"
)

// clientDirName 是 TypeScript 客户端的目录名：小写字母开头，由小写字母、数字和连字符组成。
var clientDirName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Command 构造 client 命令：调用 router 构造应用的路由，生成其中 API 的客户端。离线运行，只登记处理器；
// 需要在应用源码目录中运行（例如 go run ./cmd/blog client --out ../api），因为类型声明和错误码从源码复制。
//
// --ts 为每个 API 生成 TypeScript 客户端（见 GenerateTS），写到 --ts 目录下以 API 名称命名的子目录：
// 名称默认见 DefaultTSDir（/api 是 api，/api/admin 和 /admin/api 是 admin），--name 按 BasePath 改名，例如
// --name /api/v1=public；两个 API 名称相同时报错。--out 生成 Go 客户端，路由中有多个 API 时用 --base-path 选择一个；
// --base-path 同样把 --ts 限定为这一个 API。
//
// translations 是应用的翻译文件（与 i18n.New 的 sources 相同），生成的验证提示中它们覆盖框架内置的文案。
//
// --main 默认是当前程序的主包；--import 允许客户端直接导入的包，可重复；--rename 指定镜像包名，例如
// --rename example.com/blog/internal/session=usersession。没有生成的端点及原因输出到标准错误。
func Command(router func() chi.Routes, translations ...fs.FS) *cobra.Command {
	var gen Options
	var ts, basePath string
	var names map[string]string
	cmd := &cobra.Command{
		Use:   "client",
		Short: "生成 Go 或 TypeScript 客户端",
		Args:  command.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if gen.Out == "" && ts == "" {
				return command.AsExitError(command.ExitUsage, errors.New("--out or --ts is required"))
			}
			if gen.Main == "" {
				if info, ok := debug.ReadBuildInfo(); ok {
					gen.Main = info.Path
				}
			}
			if gen.Main == "" {
				return command.AsExitError(command.ExitUsage, errors.New("cannot tell the main package; pass --main"))
			}
			routes, err := buildRouter(router)
			if err != nil {
				return err
			}
			if ts != "" {
				apis := rest.APIs(routes)
				if basePath != "" {
					api, err := rest.Lookup(routes, basePath)
					if err != nil {
						return command.AsExitError(command.ExitUsage, err)
					}
					apis = []*rest.API{api}
				}
				dirs, err := TSDirs(apis, names)
				if err != nil {
					return command.AsExitError(command.ExitUsage, err)
				}
				for i, api := range apis {
					res, err := GenerateTS(api, TSOptions{Name: dirs[i], Translations: translations, Main: gen.Main, Dir: gen.Dir, Rename: gen.Rename})
					if err != nil {
						return err
					}
					for _, s := range res.Skipped {
						fmt.Fprintln(cmd.ErrOrStderr(), "skipped "+s)
					}
					for _, w := range res.Warnings {
						fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
					}
					if err := Write(filepath.Join(ts, dirs[i]), res.Files); err != nil {
						return err
					}
				}
			}
			if gen.Out == "" {
				return nil
			}
			api, err := rest.Lookup(routes, basePath)
			if err != nil {
				return command.AsExitError(command.ExitUsage, err)
			}
			res, err := Generate(api, gen)
			if err != nil {
				return err
			}
			for _, s := range res.Skipped {
				fmt.Fprintln(cmd.ErrOrStderr(), "skipped "+s)
			}
			return Write(gen.Out, res.Files)
		},
	}
	cmd.Flags().StringVar(&gen.Out, "out", "", "Go 客户端的输出目录，目录名是根包的包名，例如 ../api")
	cmd.Flags().StringVar(&ts, "ts", "", "TypeScript 客户端的输出目录，每个 API 一个子目录，例如 packages/client/src")
	cmd.Flags().StringToStringVar(&names, "name", nil, "BasePath=TypeScript 客户端的目录名，默认是 BasePath 中最后一个不是 api、也不是路径参数的段，可重复")
	cmd.Flags().StringVar(&gen.Main, "main", "", "应用的主包，在它的依赖中查找错误码；默认是当前程序")
	cmd.Flags().StringSliceVar(&gen.Imports, "import", nil, "客户端可以直接导入的包（含子包），可重复")
	cmd.Flags().StringVar(&basePath, "base-path", "", "只生成 BasePath 为它的 API，例如 /api/v1；路由中有多个 API 时 --out 需要它")
	cmd.Flags().StringToStringVar(&gen.Rename, "rename", nil, "服务端包路径=镜像包名，可重复")
	return command.InGroup(cmd, command.GroupDev)
}

// TSDirs 返回每个 API 的 TypeScript 客户端目录名，与 apis 一一对应：names 中按 BasePath 指定的名称，
// 否则是 DefaultTSDir。名称不合法或重复时返回错误，错误中给出在 tenon.json 中改名的写法。
func TSDirs(apis []*rest.API, names map[string]string) ([]string, error) {
	if len(apis) == 0 {
		return nil, errors.New("no API registered in the router")
	}
	for base := range names {
		if !strings.HasPrefix(base, "/") {
			return nil, errors.Newf("--name %s: base path must start with /", base)
		}
	}
	dirs := make([]string, len(apis))
	seen := map[string]string{}
	for i, api := range apis {
		base := api.BasePath()
		name, ok := names[base]
		if !ok {
			name = DefaultTSDir(base)
		}
		if !clientDirName.MatchString(name) {
			return nil, errors.Newf("API %q: client name %q must match %s; set another name in tenon.json: %s", base, name, clientDirName, renameHint(base))
		}
		if other, ok := seen[name]; ok {
			return nil, errors.Newf("APIs %q and %q are both named %q; rename one in tenon.json: %s", other, base, name, renameHint(base))
		}
		seen[name] = base
		dirs[i] = name
	}
	return dirs, nil
}

// DefaultTSDir 返回 BasePath 默认的 TypeScript 客户端目录名：从后往前第一个不是 api、也不是路径参数的段，
// 例如 /api → api、/api/v1 → v1、/api/admin → admin、/admin/api → admin、/t/{tenant}/api → t。
// 只有 api 这一段时是 api；BasePath 为空时返回 "/"，它不是合法的名称。
func DefaultTSDir(base string) string {
	segs := strings.Split(strings.Trim(base, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if s := segs[i]; s != "" && s != "api" && !strings.HasPrefix(s, "{") {
			return s
		}
	}
	return path.Base("/" + strings.Trim(base, "/"))
}

// renameHint 返回在 tenon.json 中给 BasePath 为 base 的 API 指定客户端目录名的写法。
func renameHint(base string) string {
	return fmt.Sprintf(`"apis": {%q: {"name": "<name>"}}`, base)
}

// buildRouter 调用 router 构造离线路由并捕获 panic：rest.Register 等路由校验失败直接返回原信息，
// 其他 panic（常见于构造函数使用了零值依赖）附上明确排查提示和堆栈。
func buildRouter(router func() chi.Routes) (routes chi.Routes, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		msg := fmt.Sprint(r)
		if strings.HasPrefix(msg, "rest:") {
			err = errors.Newf("build routes: %s", msg)
			return
		}
		err = errors.Newf("build routes panicked (%s): offline commands (openapi, routes, client) construct routes with zero-value dependencies; ensure RegisterRoutes and New* constructors only store dependencies without dereferencing or checking them:\n%s",
			msg, debug.Stack())
	}()
	return router(), nil
}
