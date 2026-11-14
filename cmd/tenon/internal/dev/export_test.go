package dev

import "time"

// SetPollInterval 修改检查 Go 源文件的间隔，返回恢复原值的函数。
func SetPollInterval(d time.Duration) func() {
	old := pollInterval
	pollInterval = d
	return func() { pollInterval = old }
}

// SetFrontendWait 修改等前端开发服务器的时限，返回恢复原值的函数。
func SetFrontendWait(d time.Duration) func() {
	old := frontendWait
	frontendWait = d
	return func() { frontendWait = old }
}
