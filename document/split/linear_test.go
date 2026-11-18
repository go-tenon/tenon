//go:build linux || darwin

package split

import (
	"math"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// cost 返回扫描 text 每字节的耗时（三次中最少的）和内存分配。
//
// 耗时是当前线程的 CPU 时间：机器忙时进程会被调度出去，墙上时间随之变长，而扫描大输入跨越的调度周期多，
// 被拉长得更多，两种规模的比值因此偏大。CPU 时间只计线程实际运行的时间，扫描期间把 goroutine 固定在这个线程上。
func cost(text string) (perByte time.Duration, allocated float64) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	perByte = time.Duration(math.MaxInt64)
	for range 3 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := threadTime()
		scan(text)
		elapsed := threadTime() - start
		runtime.ReadMemStats(&after)
		perByte = min(perByte, elapsed/time.Duration(len(text)))
		allocated = float64(after.TotalAlloc-before.TotalAlloc) / float64(len(text))
	}
	return max(perByte, 1), allocated
}

// TestPathologicalInputsStayLinear 确保已知的病态输入在扩大 16 倍时，每字节的耗时和内存分配基本不变；
// n^1.5 的增长会让它们变为 4 倍，平方级的增长变为 16 倍。
// 它不并行运行：同一个包中并行的测试会让计时和 runtime.MemStats 中的分配量都失真。
func TestPathologicalInputsStayLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	for name, generate := range pathological {
		smallTime, smallAlloc := cost(generate(64 << 10))
		largeTime, largeAlloc := cost(generate(1 << 20))
		if ratio := float64(largeTime) / float64(smallTime); ratio > 2.5 {
			t.Errorf("%s: time per byte grew %.1fx", name, ratio)
		}
		if ratio := largeAlloc / smallAlloc; ratio > 2 || largeAlloc > 512 {
			t.Errorf("%s: allocation per byte %.0f, grew %.1fx", name, largeAlloc, ratio)
		}
	}
}

// threadTime 返回当前线程已经使用的 CPU 时间。
func threadTime() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts); err != nil {
		panic(err)
	}
	return time.Duration(ts.Nano())
}
