import { describe, expect, it } from "vitest"
import { matchTextTrigger, replaceTriggerRange, type TextTriggerRule } from "./text-trigger"

const noop = () => {}

const mentionRule: TextTriggerRule<string> = {
  id: "mention",
  trigger: "@",
  boundary: "word",
  items: () => [],
  getKey: (x) => x,
  renderItem: (x) => x,
  onSelect: noop,
}

const macroRule: TextTriggerRule<string> = {
  id: "macro",
  trigger: "/",
  boundary: "word",
  items: () => [],
  getKey: (x) => x,
  renderItem: (x) => x,
  onSelect: noop,
}

const varRule: TextTriggerRule<string> = {
  id: "var",
  trigger: "{{",
  boundary: "any",
  stop: "}}",
  items: () => [],
  getKey: (x) => x,
  renderItem: (x) => x,
  onSelect: noop,
}

describe("matchTextTrigger", () => {
  it("在行首或空白后命中 word 边界触发符", () => {
    expect(matchTextTrigger("/refund", 7, [macroRule])).toEqual({
      ruleId: "macro",
      start: 0,
      end: 7,
      query: "refund",
    })
    expect(matchTextTrigger("你好 @张", 5, [mentionRule])).toEqual({
      ruleId: "mention",
      start: 3,
      end: 5,
      query: "张",
    })
    expect(matchTextTrigger("第一行\n/hi", 7, [macroRule])).toEqual({
      ruleId: "macro",
      start: 4,
      end: 7,
      query: "hi",
    })
  })

  it("不误触发网址和邮箱中的斜杠与 @ 符号", () => {
    expect(matchTextTrigger("请看 https://example.com/path", 27, [macroRule])).toBeNull()
    expect(matchTextTrigger("联系 foo@example.com", 18, [mentionRule])).toBeNull()
  })

  it("在中文句子中间命中 any 边界的模板变量触发符，并在遇到 stop 闭合后停止", () => {
    expect(matchTextTrigger("您好{{con", 7, [varRule])).toEqual({
      ruleId: "var",
      start: 2,
      end: 7,
      query: "con",
    })
    expect(matchTextTrigger("您好{{contact.name}}谢谢", 20, [varRule])).toBeNull()
    expect(matchTextTrigger("您好{{contact.name}}，{{em", 23, [varRule])).toEqual({
      ruleId: "var",
      start: 19,
      end: 23,
      query: "em",
    })
  })

  it("同时存在多条规则时取最靠近光标的触发符，并忽略禁用的规则", () => {
    const rules = [mentionRule, macroRule, varRule]
    expect(matchTextTrigger("@张三 请看 /ord", 11, rules)).toEqual({
      ruleId: "macro",
      start: 7,
      end: 11,
      query: "ord",
    })
    expect(matchTextTrigger("@张三 请看 {{na", 11, rules)).toEqual({
      ruleId: "var",
      start: 7,
      end: 11,
      query: "na",
    })
    expect(matchTextTrigger("/hi", 3, [{ ...macroRule, enabled: false }])).toBeNull()
  })

  it("出现空白或换行后结束未允许空格的触发", () => {
    expect(matchTextTrigger("/hi there", 9, [macroRule])).toBeNull()
    expect(matchTextTrigger("/hi there", 9, [{ ...macroRule, allowSpace: true }])).toEqual({
      ruleId: "macro",
      start: 0,
      end: 9,
      query: "hi there",
    })
  })
})

describe("replaceTriggerRange", () => {
  it("替换指定区间并计算替换后的光标位置", () => {
    expect(replaceTriggerRange("你好 /ref，请查收", 3, 7, "退款已发起")).toEqual({
      value: "你好 退款已发起，请查收",
      caret: 8,
    })
  })

  it("在 start === end 时按光标处插入（命令式打开场景）", () => {
    expect(replaceTriggerRange("您好，", 2, 2, "{{contact.name}}")).toEqual({
      value: "您好{{contact.name}}，",
      caret: 18,
    })
  })
})
