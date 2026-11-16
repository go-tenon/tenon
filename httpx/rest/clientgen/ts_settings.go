package clientgen

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/httpx/rest"
)

// settingsTS 生成 settings.ts：rest.API.Settings 声明的设置页面，字段的类型、枚举和是否必填取自保存接口的请求体，
// 显示文字取自翻译文件中 <名称>.title、<名称>.fields.<字段> 的消息。缺少显示文字时返回警告，与资源相同。
func (t *tsGen) settingsTS(templates map[string]map[string]string) (string, []string, error) {
	var b strings.Builder
	var warnings []string
	b.WriteString(`/**
 * 设置：服务端用 rest.API.Settings 声明的设置页面，@go-tenon/shell 的 settingsModel 据此渲染设置表单。
 * operations 是客户端方法的位置（client[分组][方法]），schemas.ts、queries.ts 中的位置相同；
 * 字段的 type、format、enum（enums.ts 中的命名类型）和 required 取自保存接口的请求体；
 * permissions 是两个接口要求的权限；labels 按语言列出显示文字，缺少时先用默认语言，再用字段名。
 */
export const settings = {
`)
	for _, s := range t.api.SettingsPages() {
		src, warn, err := t.settingTS(s, templates)
		if err != nil {
			return "", nil, errors.Wrapf(err, "clientgen: settings %s", s.Name)
		}
		warnings = append(warnings, warn...)
		b.WriteString(src)
	}
	b.WriteString("} as const\n")
	return b.String(), warnings, nil
}

func (t *tsGen) settingTS(s rest.Settings, templates map[string]map[string]string) (string, []string, error) {
	var b strings.Builder
	b.WriteString("  " + tsKey(s.Name) + ": {\n")
	b.WriteString("    name: " + strconv.Quote(s.Name) + ",\n")
	get, err := t.findOp(s.Get)
	if err != nil {
		return "", nil, err
	}
	update, err := t.findOp(s.Update)
	if err != nil {
		return "", nil, err
	}
	b.WriteString("    operations: { get: " + opRef(get) + ", update: " + opRef(update) + " },\n")
	b.WriteString("    permissions: { get: " + required(get) + ", update: " + required(update) + " },\n")
	body := t.requestBody(update.op)
	b.WriteString("    form: [\n")
	for _, f := range s.Form {
		b.WriteString("      " + t.fieldTS(f, body.Properties[f], slices.Contains(body.Required, f), false) + ",\n")
	}
	b.WriteString("    ],\n")

	// 显示文字。
	keys := []string{s.Name + ".title"}
	for _, f := range s.Form {
		keys = append(keys, s.Name+".fields."+f)
	}
	var warnings []string
	var langs strings.Builder
	for _, lang := range slices.Sorted(maps.Keys(templates)) {
		msgs := templates[lang]
		var missing []string
		for _, k := range keys {
			if _, ok := msgs[k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) == len(keys) {
			continue
		}
		if len(missing) > 0 {
			warnings = append(warnings, fmt.Sprintf("settings %s has no %s label for %s", s.Name, lang, strings.Join(missing, ", ")))
		}
		langs.WriteString("      " + strconv.Quote(lang) + ": {\n")
		if title, ok := msgs[s.Name+".title"]; ok {
			langs.WriteString("        title: " + strconv.Quote(title) + ",\n")
		}
		langs.WriteString("        fields: { " + labels(msgs, s.Name+".fields.", s.Form) + " },\n")
		langs.WriteString("      },\n")
	}
	if langs.Len() == 0 {
		warnings = append(warnings, fmt.Sprintf("settings %s has no labels; add messages %s.title and %s.fields.<field> to the translations", s.Name, s.Name, s.Name))
	}
	b.WriteString("    labels: {\n" + langs.String() + "    },\n")
	b.WriteString("  },\n")
	return b.String(), warnings, nil
}
