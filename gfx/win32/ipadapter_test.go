package win32

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// ===== 合成 GetAdaptersInfo 回填缓冲 =====
//
// 这些辅助函数存在的理由: 网卡枚举只能在 Windows 上真跑, 而 2026-10-08 那次事故
// (看板 rMWHi1) 的全部代价恰恰来自"只能上真机验证, 崩的时候连测试都起不来"。
// 缓冲是**纯字节**, 而 IP_ADAPTER_INFO 的布局由 GOARCH 决定 (与 GOOS 无关), 所以
// 造一块形状正确的缓冲就能在 Linux 上把解析逻辑整条跑一遍 —— 包括"指针不可信"
// 这类真机上难以稳定复现的分支。

type synthRec struct {
	typ uint32
	ips []string // 该记录的 IP_ADDR_STRING 链 (第一个节点是内嵌的 IpAddressList)
}

func putU32(b []byte, off uintptr, v uint32) {
	binary.LittleEndian.PutUint32(b[off:off+4], v)
}

// putPtr 按**当前字长**写一个指针宽度的值 (386 上写 8 字节会越界/串字段)。
func putPtr(b []byte, off uintptr, v uintptr) {
	for i := uintptr(0); i < ptrSize; i++ {
		b[off+i] = byte(v >> (8 * i))
	}
}

// synthAdapterBuffer 造一块由 recs 串成的记录链 (Next 指向同缓冲内的下一条)。
func synthAdapterBuffer(recs []synthRec) []byte {
	if len(recs) == 0 {
		return nil
	}
	buf := make([]byte, len(recs)*int(ipAdapterInfoSize))
	base := uintptr(unsafe.Pointer(&buf[0]))
	for i, r := range recs {
		off := uintptr(i) * ipAdapterInfoSize
		putU32(buf, off+offAdapterType, r.typ)
		for j, ip := range r.ips {
			node := off + offIpAddressList + uintptr(j)*ipAddrStringSize
			copy(buf[node+offAddrStringIP:], ip)
			if j+1 < len(r.ips) {
				putPtr(buf, node+offAddrStringNext, base+node+ipAddrStringSize)
			}
		}
		if i+1 < len(recs) {
			putPtr(buf, off, base+off+ipAdapterInfoSize)
		}
	}
	return buf
}

// ===== 布局 =====

// TestIPAdapterLayoutMatchesSDK 把 SDK (iptypes.h 按 MSVC 对齐规则展开) 的期望偏移
// 钉成断言。
//
// ipadapter.go 里已经有一份**编译期**自证 (布局漂移 = 编译不过), 这里再跑一遍是
// 为了让 `go test ./gfx/win32/` (Linux 也能跑) 也红一次 —— 编译期断言只在编译那
// 个 GOOS 时生效, 而布局本身只跟 GOARCH 有关。
func TestIPAdapterLayoutMatchesSDK(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"AdapterName", offAdapterName, wantOffAdapterName},
		{"Description", offDescription, wantOffDescription},
		{"AddressLength", offAddressLength, wantOffAddressLength},
		{"Index", offAdapterIndex, wantOffAdapterIndex},
		{"Type", offAdapterType, wantOffAdapterType},
		{"DhcpEnabled", offDhcpEnabled, wantOffDhcpEnabled},
		{"CurrentIpAddress", offCurrentIPAddr, wantOffCurrentIPAddr},
		{"IpAddressList", offIpAddressList, wantOffIpAddressList},
		{"sizeof(IP_ADAPTER_INFO)", ipAdapterInfoSize, wantIpAdapterInfo},
		{"sizeof(IP_ADDR_STRING)", ipAddrStringSize, wantIpAddrString},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s 偏移应为 %d (SDK), 实际 %d", c.name, c.want, c.got)
		}
	}
	// 我们真正会读的区间必须落在一条记录之内 (读过头就是别人的字段)。
	if ipAdapterInfoReadSpan > ipAdapterInfoSize {
		t.Fatalf("读取区间 %d 超出单条记录 %d", ipAdapterInfoReadSpan, ipAdapterInfoSize)
	}
	// 关键字段的相对顺序 (旧实现正是把 IpAddressList 放到了 DhcpEnabled 之前)。
	if !(offAdapterType < offDhcpEnabled && offDhcpEnabled < offCurrentIPAddr &&
		offCurrentIPAddr < offIpAddressList) {
		t.Fatalf("字段顺序与 SDK 不符: type=%d dhcp=%d cur=%d list=%d",
			offAdapterType, offDhcpEnabled, offCurrentIPAddr, offIpAddressList)
	}
}

// ===== 解析 =====

func TestNetworkTypeFromBuffer(t *testing.T) {
	tests := []struct {
		name     string
		recs     []synthRec
		wantConn bool
		wantTyp  string
	}{
		{"空缓冲", nil, false, "none"},
		{"以太网卡带地址", []synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"192.168.1.5"}}}, true, "ethernet"},
		{"无线网卡带地址", []synthRec{{typ: ifTypeIEEE80211, ips: []string{"10.0.0.3"}}}, true, "wifi"},
		{"PPP 拨号", []synthRec{{typ: ifTypePPP, ips: []string{"10.1.1.1"}}}, true, "vpn"},
		{"类型未知但有地址", []synthRec{{typ: 999, ips: []string{"10.1.1.1"}}}, true, "other"},
		{"无地址", []synthRec{{typ: ifTypeEthernetCSMACD}}, false, "none"},
		{"0.0.0.0 不算地址", []synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"0.0.0.0"}}}, false, "none"},
		{"网卡描述不是 IP", []synthRec{{typ: ifTypeIEEE80211, ips: []string{"Realtek PCIe GB"}}}, false, "none"},
		{"链第二跳才有地址", []synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"0.0.0.0", "172.16.0.9"}}}, true, "ethernet"},
		{"多网卡优先 wifi", []synthRec{
			{typ: ifTypeEthernetCSMACD, ips: []string{"10.0.0.2"}},
			{typ: ifTypeIEEE80211, ips: []string{"10.0.0.3"}},
		}, true, "wifi"},
		{"只有第二条有地址", []synthRec{
			{typ: ifTypeEthernetCSMACD},
			{typ: ifTypeIEEE80211, ips: []string{"10.0.0.9"}},
		}, true, "wifi"},
	}
	for _, tc := range tests {
		connected, typ := networkTypeFromBuffer(synthAdapterBuffer(tc.recs))
		if connected != tc.wantConn || typ != tc.wantTyp {
			t.Errorf("%s: 得 (%v,%q), 要 (%v,%q)", tc.name, connected, typ, tc.wantConn, tc.wantTyp)
		}
	}
}

// TestNetworkTypeFromBufferRespectsBackfilledSize 钉住"变长结构必须以回填 size 为
// 硬边界"这条 (事故的第二个缺陷: 旧代码只判 GetAdaptersInfo 返回值, 不看回填
// size)。IP_ADAPTER_INFO 链表的最后一条常常是不完整的, 读到它就是读到别人的字节。
func TestNetworkTypeFromBufferRespectsBackfilledSize(t *testing.T) {
	buf := synthAdapterBuffer([]synthRec{
		{typ: ifTypeEthernetCSMACD, ips: []string{"192.168.1.5"}},
		{typ: ifTypeIEEE80211, ips: []string{"10.0.0.3"}},
	})
	tests := []struct {
		name     string
		size     uintptr
		wantConn bool
		wantTyp  string
	}{
		{"size 连一条记录都不到", ipAdapterInfoReadSpan - 1, false, "none"},
		{"size 刚好覆盖一条", ipAdapterInfoReadSpan, true, "ethernet"},
		{"第二条不完整: 不读", ipAdapterInfoSize + 4, true, "ethernet"},
		{"两条都完整", 2 * ipAdapterInfoSize, true, "wifi"},
	}
	for _, tc := range tests {
		connected, typ := networkTypeFromBuffer(buf[:tc.size])
		if connected != tc.wantConn || typ != tc.wantTyp {
			t.Errorf("%s: 得 (%v,%q), 要 (%v,%q)", tc.name, connected, typ, tc.wantConn, tc.wantTyp)
		}
	}
}

// TestNetworkTypeFromBufferUntrustedNext 钉住"Next 指针只换算、不解引用"这条。
//
// 事故现场就是它: 旧实现在一个错位偏移上读出一段普通数据, 当成 IP_ADDR_STRING*
// 解引用 ⇒ fault addr 0x322b。真机上这类指针要靠"恶心的数据"才能复现, 合成缓冲
// 能稳定造出来。
func TestNetworkTypeFromBufferUntrustedNext(t *testing.T) {
	base := func(b []byte) uintptr { return uintptr(unsafe.Pointer(&b[0])) }

	buf := synthAdapterBuffer([]synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"192.168.1.5"}}})
	// 指向缓冲外 (远得离谱, 但仍在 uintptr 范围内 —— 386 上 uintptr 只有 32 位)。
	putPtr(buf, 0, base(buf)+8*maxAdapterBufferSize)
	if connected, typ := networkTypeFromBuffer(buf); !connected || typ != "ethernet" {
		t.Fatalf("Next 指向缓冲外时仍应读出第一条: 得 (%v,%q)", connected, typ)
	}

	buf = synthAdapterBuffer([]synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"192.168.1.5"}}})
	putPtr(buf, 0, base(buf)-8) // 指向缓冲之前 (delta 回绕成巨数)
	if connected, typ := networkTypeFromBuffer(buf); !connected || typ != "ethernet" {
		t.Fatalf("Next 指向缓冲之前时仍应读出第一条: 得 (%v,%q)", connected, typ)
	}

	// 自引用: 链成一个环。必须靠 maxAdapterRecords 收敛, 而不是死循环。
	buf = synthAdapterBuffer([]synthRec{{typ: ifTypeEthernetCSMACD, ips: []string{"192.168.1.5"}}})
	putPtr(buf, 0, base(buf))
	if connected, typ := networkTypeFromBuffer(buf); !connected || typ != "ethernet" {
		t.Fatalf("Next 自引用时应被遍历上限收敛: 得 (%v,%q)", connected, typ)
	}
}

// TestNetworkTypeFromBufferIgnoresIPAtWrongOffset 是"布局错位"这条缺陷直接的钉子。
//
// 做法与"断言某个魔数偏移"不同: 除了 IpAddressList.IpAddress 那 16 字节之外, 记录
// 里**每个** 16 字节对齐的位置都塞一个合法 IPv4。于是只要实现从任何一个错误的
// 偏移读地址串, 它都会命中一个 IP 并谎报"已连接" —— 谎报比崩溃更难查 (症状是
// network() 永远说连着, 而没人会怀疑偏移)。
func TestNetworkTypeFromBufferIgnoresIPAtWrongOffset(t *testing.T) {
	buf := synthAdapterBuffer([]synthRec{{typ: ifTypeEthernetCSMACD}})
	target := offIpAddressList + offAddrStringIP
	const ip = "10.0.0.7"
	for off := uintptr(0); off+ipAddressStringLen <= ipAdapterInfoSize; off += ipAddressStringLen {
		if off < target+ipAddressStringLen && off+ipAddressStringLen > target {
			continue // 正确位置留给"不是 IP"的内容
		}
		copy(buf[off:], ip)
	}
	copy(buf[target:], "not-an-ip-xx")
	if connected, typ := networkTypeFromBuffer(buf); connected || typ != "none" {
		t.Fatalf("只有错误偏移上有 IP 时不得报已连接, 得 (%v,%q)", connected, typ)
	}
}

// ===== 缓冲读取的边界 =====

func TestIPBufReadsAreBoundsChecked(t *testing.T) {
	// 空缓冲: 任何读取都必须返回零值 (失败模式是"降级成未连接", 不是 panic)。
	empty := ipBuf{}
	if empty.size() != 0 || empty.base() != 0 {
		t.Fatalf("空缓冲 size/base 应为 0")
	}
	if got := empty.u32(0); got != 0 {
		t.Fatalf("空缓冲 u32 应为 0, 实际 %d", got)
	}
	if got := empty.ptr(0); got != 0 {
		t.Fatalf("空缓冲 ptr 应为 0, 实际 %d", got)
	}
	if got := empty.cstr(0, ipAddressStringLen); got != "" {
		t.Fatalf("空缓冲 cstr 应为空串, 实际 %q", got)
	}
	if empty.hasIPv4(0) {
		t.Fatalf("空缓冲 hasIPv4 应为 false")
	}

	// 短缓冲: 记录头都不完整时也必须安全返回。
	short := ipBuf{b: make([]byte, 8)}
	if got := short.u32(6); got != 0 {
		t.Fatalf("越界 u32 应为 0, 实际 %d", got)
	}
	if got := short.ptr(6); got != 0 {
		t.Fatalf("越界 ptr 应为 0, 实际 %d", got)
	}
	if got := short.cstr(2, ipAddressStringLen); got != "" {
		t.Fatalf("越界 cstr 应为空串, 实际 %q", got)
	}
	if short.hasIPv4(0) {
		t.Fatalf("短缓冲 hasIPv4 应为 false")
	}

	// 读法: u32 是小端。
	b := ipBuf{b: []byte{1, 2, 3, 4, 0, 0, 0, 0}}
	if got := b.u32(0); got != 0x04030201 {
		t.Fatalf("u32 应为小端 0x04030201, 实际 %#x", got)
	}
	// ptr 按**字长**读 (不是定长 8 字节): 386 上读 8 字节会串到下一个字段。
	pb := make([]byte, 2*ptrSize)
	var want uintptr
	for i := uintptr(0); i < ptrSize; i++ {
		pb[i] = byte(i + 1)
		want |= uintptr(i+1) << (8 * i)
	}
	if got := (ipBuf{b: pb}).ptr(0); got != want {
		t.Fatalf("ptr(0) = %#x, 要 %#x (字长 %d)", got, want, ptrSize)
	}
}

func TestLooksLikeIPv4(t *testing.T) {
	tests := map[string]bool{
		"1.2.3.4":     true,
		"0.0.0.0":     true, // 形状合法; "不算地址"的判定在上层 (hasIPv4)
		"255.255.0.1": true,
		"01.2.3.4":    true,
		"256.1.1.1":   false,
		"1.2.3":       false,
		"1.2.3.4.5":   false,
		"1.2.3.a":     false,
		"":            false,
		"Realtek":     false,
		"1..2.3":      false,
	}
	for in, want := range tests {
		if got := looksLikeIPv4(in); got != want {
			t.Errorf("looksLikeIPv4(%q) = %v, 要 %v", in, got, want)
		}
	}
}
