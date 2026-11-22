package clientgen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/httpx/rest"
)

// settingsTS 生成 settings.ts：rest.API.Settings 声明的设置页面，字段的类型、枚举和是否必填取自保存接口的请求体，
// 源语言（defaultLang）的显示文字取自 Settings.Title、Labels、Descriptions，翻译文件中 <名称>.title、
// <名称>.fields.<字段>、<名称>.descriptions.<字段> 优先覆盖；其他语言取自翻译文件。缺少显示文字时返回警告。
func (t *tsGen) settingsTS(templates map[string]map[string]string, defaultLang string) (string, []string, error) {
	var b strings.Builder
	var warnings []string
	b.WriteString(`/**
 * 设置：服务端用 rest.API.Settings 声明的设置页面，@go-tenon/ui 的 settingsModel 据此渲染设置表单。
 * operations 是客户端方法的位置（client[分组][方法]），schemas.ts、queries.ts 中的位置相同；
 * 字段的 type、format、enum（enums.ts 中的命名类型）和 required 取自保存接口的请求体；
 * permissions 是两个接口要求的权限；labels 按语言列出显示文字，缺少时先用默认语言，再用字段名；
 * descriptions 是字段的说明，显示在输入框下面。
 */
export const settings = {
`)
	known := t.knownResources()
	for _, s := range t.api.SettingsPages() {
		src, warn, err := t.settingTS(s, templates, defaultLang, known)
		if err != nil {
			return "", nil, errors.Wrapf(err, "clientgen: settings %s", s.Name)
		}
		warnings = append(warnings, warn...)
		b.WriteString(src)
	}
	b.WriteString("} as const\n")
	return b.String(), warnings, nil
}

// settingTS 返回设置页 s 的 TypeScript 描述，以及它用到的类型。
func (t *tsGen) settingTS(s rest.Settings, templates map[string]map[string]string, defaultLang string, known map[string]bool) (string, []string, error) {
	if err := checkRefs(s.Refs, known); err != nil {
		return "", nil, err
	}
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
		b.WriteString("      " + t.fieldTS(f, body.Properties[f], slices.Contains(body.Required, f), false, s.Refs[f]) + ",\n")
	}
	b.WriteString("    ],\n")

	// 显示文字。
	keys := []string{s.Name + ".title"}
	for _, f := range s.Form {
		keys = append(keys, s.Name+".fields."+f)
	}
	var warnings []string
	var langs strings.Builder
	inline := settingsInlineMessages(s)
	for _, lang := range candidateLanguages(templates, defaultLang) {
		msgs := templates[lang]
		if lang == defaultLang && len(inline) > 0 {
			msgs = mergeMessages(inline, msgs)
		}
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
		if d := labels(msgs, s.Name+".descriptions.", s.Form); d != "" {
			langs.WriteString("        descriptions: { " + d + " },\n")
		}
		langs.WriteString("      },\n")
	}
	if langs.Len() == 0 {
		warnings = append(warnings, fmt.Sprintf("settings %s has no labels; set Title/Labels on rest.Settings or add messages %s.title and %s.fields.<field> to the translations", s.Name, s.Name, s.Name))
	}
	b.WriteString("    labels: {\n" + langs.String() + "    },\n")
	b.WriteString("  },\n")
	return b.String(), warnings, nil
}

// settingsInlineMessages 把 rest.Settings 上内联的默认标题、字段名和字段说明转为同键名的消息映射。
func settingsInlineMessages(s rest.Settings) map[string]string {
	out := map[string]string{}
	if s.Title != "" {
		out[s.Name+".title"] = s.Title
	}
	for f, l := range s.Labels {
		if l != "" {
			out[s.Name+".fields."+f] = l
		}
	}
	for f, d := range s.Descriptions {
		if d != "" {
			out[s.Name+".descriptions."+f] = d
		}
	}
	return out
}
