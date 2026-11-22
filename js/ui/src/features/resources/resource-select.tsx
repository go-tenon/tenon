// 关联资源选择器：基于资源模型（model.ts 的 resourceModel），自动翻页拉取全部列表项，
// 支持按资源权限控制请求、本地搜索、自定义选项映射与自定义选项渲染。

import type { FetchAllPagesOptions } from "@go-tenon/client"
import { Loader, MultiSelect, type MultiSelectProps, Select, type SelectProps } from "@mantine/core"
import { useQuery } from "@tanstack/react-query"
import { type ReactNode, useMemo } from "react"
import { useCan } from "../../app/app"
import { useText } from "../../i18n/i18n"
import type { ResourceModel, ResourceRow } from "./model"

/** 资源选择器的一个选项，与 Mantine Select、MultiSelect 的 ComboboxItem 兼容。 */
export interface ResourceOption {
  value: string
  label: string
  disabled?: boolean
}

/** ResourceSelect 与 ResourceMultiSelect 共用的选项。 */
export interface ResourceSelectBaseProps {
  /** 要选择的资源模型。 */
  model: ResourceModel
  /** 传给列表接口的附加查询参数（例如筛选条件、排序）。 */
  params?: Record<string, unknown>
  /** 翻页拉取的选项（每页条数、最大页数、并发数）。 */
  fetchOptions?: FetchAllPagesOptions
  /** 是否启用数据加载，默认 true；同时要求具备资源的 list 权限。 */
  enabled?: boolean
  /** 从每行取显示文字的字段名；缺省时依次取资源第一列、name、title、label 和行标识。 */
  labelField?: string
  /** 过滤要显示的行：返回 true 的行保留在选项中。 */
  filterRow?: (row: ResourceRow) => boolean
  /** 把一行转换为下拉选项；返回 null 或 undefined 时跳过这一行。 */
  getOption?: (row: ResourceRow) => ResourceOption | null | undefined
  /** 自定义下拉选项的渲染内容，接收对应的原始行数据；与 Mantine 的 renderOption 二选一。 */
  renderRow?: (row: ResourceRow, state: { option: ResourceOption; checked?: boolean }) => ReactNode
}

/** ResourceSelect 的属性：继承 Mantine Select 的全部属性，data 由资源模型提供。 */
export type ResourceSelectProps = Omit<SelectProps, "data"> & ResourceSelectBaseProps

/** ResourceMultiSelect 的属性：继承 Mantine MultiSelect 的全部属性，data 由资源模型提供。 */
export type ResourceMultiSelectProps = Omit<MultiSelectProps, "data"> & ResourceSelectBaseProps

const emptyRows: readonly ResourceRow[] = []

/** 从资源行推导选项的显示文字：优先 labelField，其次资源第一列（经 model.display 转换）、name、title、label、行标识。 */
function defaultLabel(model: ResourceModel, row: ResourceRow, value: string, labelField?: string): string {
  if (labelField) {
    const v = row[labelField]
    if (v !== undefined && v !== null && String(v) !== "") return String(model.display(labelField, v))
    return value
  }
  const first = model.columns[0]
  if (first) {
    const v = row[first.name]
    if (v !== undefined && v !== null && String(v) !== "") return String(model.display(first, v))
  }
  for (const key of ["name", "title", "label"]) {
    const v = row[key]
    if (v !== undefined && v !== null && String(v) !== "") return String(v)
  }
  return value
}

/**
 * 由资源行列表构建下拉选项和行索引：按 filterRow 过滤，按 getOption 或 labelField 映射，
 * 并按 value 去重，避免分页期间数据偏移产生重复选项。
 */
export function buildResourceOptions(
  model: ResourceModel,
  rows: readonly ResourceRow[],
  options: Pick<ResourceSelectBaseProps, "labelField" | "filterRow" | "getOption"> = {},
): { items: ResourceOption[]; rowsByValue: Map<string, ResourceRow> } {
  const { labelField, filterRow, getOption } = options
  const items: ResourceOption[] = []
  const rowsByValue = new Map<string, ResourceRow>()
  for (const row of rows) {
    if (filterRow && !filterRow(row)) continue
    let opt: ResourceOption | null | undefined
    if (getOption) {
      opt = getOption(row)
    } else {
      const value = model.rowKey(row)
      opt = { value, label: defaultLabel(model, row, value, labelField) }
    }
    if (!opt || rowsByValue.has(opt.value)) continue
    rowsByValue.set(opt.value, row)
    items.push(opt)
  }
  return { items, rowsByValue }
}

/** 资源选择器的共用数据逻辑：检查列表权限、翻页拉取全量列表并组装下拉选项和状态文案。 */
function useResourceSelectData({
  model,
  params,
  fetchOptions,
  enabled = true,
  labelField,
  filterRow,
  getOption,
  renderRow,
  renderOption,
  rightSection,
  nothingFoundMessage,
}: ResourceSelectBaseProps & {
  renderOption?: SelectProps["renderOption"]
  rightSection?: ReactNode
  nothingFoundMessage?: ReactNode
}) {
  const can = useCan()
  const t = useText()
  const allowed = model.allowed(can, "list")
  const queryEnabled = enabled && allowed
  const query = useQuery({ ...model.listAllQuery(params, fetchOptions), enabled: queryEnabled })
  const rows = query.data ?? emptyRows
  const { items, rowsByValue } = useMemo(
    () => buildResourceOptions(model, rows, { labelField, filterRow, getOption }),
    [model, rows, labelField, filterRow, getOption],
  )
  const loading = queryEnabled && query.isPending
  const resolvedRightSection = rightSection !== undefined ? rightSection : loading ? <Loader size="xs" /> : undefined
  const resolvedNothingFound =
    nothingFoundMessage !== undefined
      ? nothingFoundMessage
      : query.isError
        ? t("tenon.resource.load_failed")
        : loading
          ? t("tenon.common.loading")
          : t("tenon.resource.no_options")
  const resolvedRenderOption: SelectProps["renderOption"] = renderRow
    ? (input) => {
        const row = rowsByValue.get(input.option.value)
        return row ? renderRow(row, { option: input.option, checked: input.checked }) : input.option.label
      }
    : renderOption
  return {
    data: items,
    rightSection: resolvedRightSection,
    nothingFoundMessage: resolvedNothingFound,
    renderOption: resolvedRenderOption,
  }
}

/**
 * 关联资源的单选下拉框：通过 model.listAllQuery() 翻页加载全部选项并默认开启本地搜索，
 * 自动检查资源的 list 权限。可以直接展开 @mantine/form 的 getInputProps：
 *
 *   <ResourceSelect model={providers} label="供应商" {...form.getInputProps("provider_id")} />
 */
export function ResourceSelect({
  model,
  params,
  fetchOptions,
  enabled,
  labelField,
  filterRow,
  getOption,
  renderRow,
  searchable = true,
  renderOption,
  rightSection,
  nothingFoundMessage,
  ...rest
}: ResourceSelectProps): ReactNode {
  const resolved = useResourceSelectData({
    model,
    params,
    fetchOptions,
    enabled,
    labelField,
    filterRow,
    getOption,
    renderRow,
    renderOption,
    rightSection,
    nothingFoundMessage,
  })
  return <Select {...rest} searchable={searchable} {...resolved} />
}

/**
 * 关联资源的多选下拉框：通过 model.listAllQuery() 翻页加载全部选项并默认开启本地搜索，
 * 自动检查资源的 list 权限。可以直接展开 @mantine/form 的 getInputProps：
 *
 *   <ResourceMultiSelect model={models} label="允许的模型" {...form.getInputProps("model_ids")} />
 */
export function ResourceMultiSelect({
  model,
  params,
  fetchOptions,
  enabled,
  labelField,
  filterRow,
  getOption,
  renderRow,
  searchable = true,
  renderOption,
  rightSection,
  nothingFoundMessage,
  ...rest
}: ResourceMultiSelectProps): ReactNode {
  const resolved = useResourceSelectData({
    model,
    params,
    fetchOptions,
    enabled,
    labelField,
    filterRow,
    getOption,
    renderRow,
    renderOption,
    rightSection,
    nothingFoundMessage,
  })
  return <MultiSelect {...rest} searchable={searchable} {...resolved} />
}
