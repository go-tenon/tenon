package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// RealIP 返回把 r.RemoteAddr 改写为真实客户端 IP 的中间件。
//
// 只有直连对端位于 trusted 网段时才读取代理头，否则 RemoteAddr 保持不变，
// 以防客户端伪造 X-Forwarded-For。解析规则：
//
//   - 从右向左遍历 X-Forwarded-For，跳过可信代理，第一个不可信的地址即客户端；
//     全部可信时取最左边的地址；遇到无法解析的条目则停止，并保持 RemoteAddr 不变。
//   - 没有 X-Forwarded-For 时使用 X-Real-IP。
//
// 改写后的 RemoteAddr 只有 IP、不带端口（与 chi 的 RealIP 一致），
// 读取时请用 ClientIP，它对两种格式都适用。trusted 为空时本中间件不做任何事。
func RealIP(trusted ...netip.Prefix) func(http.Handler) http.Handler {
	trusted = append([]netip.Prefix(nil), trusted...)
	isTrusted := func(ip netip.Addr) bool {
		ip = ip.Unmap()
		for _, p := range trusted {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}

	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peer, ok := parseIP(r.RemoteAddr); ok && isTrusted(peer) {
				if ip, ok := forwardedIP(r.Header, isTrusted); ok {
					r2 := new(http.Request)
					*r2 = *r
					r2.RemoteAddr = ip.String()
					r = r2
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func forwardedIP(h http.Header, isTrusted func(netip.Addr) bool) (netip.Addr, bool) {
	var hops []string
	for _, v := range h.Values("X-Forwarded-For") {
		for hop := range strings.SplitSeq(v, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	if len(hops) == 0 {
		if v := strings.TrimSpace(h.Get("X-Real-IP")); v != "" {
			hops = []string{v}
		}
	}

	var last netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(hops[i])
		if err != nil {
			return netip.Addr{}, false
		}
		ip = ip.Unmap().WithZone("")
		if !isTrusted(ip) {
			return ip, true
		}
		last = ip
	}
	return last, last.IsValid()
}

// ClientIP 返回请求的客户端 IP 字符串：经过 RealIP 时为真实客户端 IP，
// 否则为连接对端 IP。r.RemoteAddr 无法解析时原样返回。
func ClientIP(r *http.Request) string {
	if ip, ok := parseIP(r.RemoteAddr); ok {
		return ip.String()
	}
	return r.RemoteAddr
}

// parseIP 解析 "ip"、"ip:port" 或 "[ipv6]:port" 格式的地址。
func parseIP(addr string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap().WithZone(""), true
}
