package rest

// PageInput 是游标分页的请求参数，嵌入到接口的输入中：
//
//	type listInput struct {
//		rest.PageInput
//		Status string `query:"status"`
//	}
type PageInput struct {
	Before string `query:"before" format:"uuid" doc:"上一页返回的 next，省略表示第一页"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"每页数量"`
}

// Page 是游标分页的响应：按创建时间倒序，next 省略表示没有更多。
type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty" format:"uuid" doc:"下一页的 before 参数，没有更多时省略"`
}
