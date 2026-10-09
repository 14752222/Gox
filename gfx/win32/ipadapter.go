package win32

// ===== GetAdaptersInfo / IP_ADAPTER_INFO: 布局与解析 =====
//
// ## 这个文件为什么单独拎出来
//
// 2026-10-08 的 P0 事故 (看板 rMWHi1) 全部发生在网卡枚举这一段: `gox test262`
// 这种**完全不用 GUI** 的路径也 100% 启动即 SIGSEGV, fault addr 恒定 0x322b。
// 宿主该报什么放在 host.go; 这里专门放"最容易崩的那一块", 好让三条纪律在一个
// 文件里说清楚:
//
//	1. 布局 —— 不许再手写 struct 拿点号访问 C 回填的缓冲 (见 ipAdapterInfoABI
//	   的注释): 布局错位在这里的症状是顶层的段错误, 而不是某块代码报错;
//	2. 边界 —— 结构体是**变长**的: GetAdaptersInfo 把若干条记录串成链表回填进
//	   一块缓冲 (Next 指向同缓冲内的下一条), 所以"缓冲到底多长"只能信内核回填
//	   的 size, 而且必须与自己的缓冲区长度取小者;
//	3. 时机 —— 不在包 init() 里枚举 (见 host.go 的 reportNetworkLazy): 把崩溃面
//	   从"进程起不来"收回"network() 这一个能力"。
//
// ## 零 cgo / 零新依赖
//
// 偏移全部由标准库 unsafe 在**编译期**算出。没用 golang.org/x/sys/windows 的现成
// 定义有两个原因: 一是它在 go.mod 里是 indirect 依赖, 引进来会把 GUI 后端的依赖
// 面撑大; 二是我们真正需要的只是"80186 之后的 MSVC 与 Go 都遵守的那几句对齐规则",
// unsafe.Offsetof 已经够 —— 而且下面那段编译期自证会在两者不一致时直接编译失败。
//
// ## 为什么这个文件**不带** windows 构建标签
//
// 布局与解析这一段**不碰任何 Win32 符号**: 偏移由 unsafe 在编译期算出, 解析只是
// 在 []byte 上做带边界的读 (连 Next 指针都只换算不解引用)。所以把它留在无标签的
// 文件里, 这段最容易写错的逻辑就能在 Linux 上直接 `go test` (见 ipadapter_test.go)
// —— 上次事故的全部代价都来自"它只能在 Windows 上验证, 而崩的时候连测试二进制
// 都起不来"。真正的 syscall (GetAdaptersInfo / hostNetworkType) 在
// ipadapter_windows.go。

import (
	"strings"
	"unsafe"
)

// ===== 布局: IP_ADDR_STRING / IP_ADAPTER_INFO 的逐字段复刻 =====

// iptypes.h 的长度上限。写死成常量而不是"看着差不多的值": 数组长度写错正是上次
// 事故的根因 (旧结构体把 AdapterName 写成 [132]byte, 于是后面每个字段都错了)。
const (
	maxAdapterNameLength        = 256
	maxAdapterDescriptionLength = 128
	maxAdapterAddressLength     = 8

	// ipAddressStringLen 是 IP_ADDRESS_STRING ("255.255.255.0\0" 的那种) 的长度。
	ipAddressStringLen = 16
)

// ipAddrStringABI 对应 IP_ADDR_STRING。
//
// ⚠️ 这一组类型**只用于取偏移和长度** (unsafe.Offsetof/Sizeof), 永远不要把
// GetAdaptersInfo 的回填缓冲强转成 *ipAdapterInfoABI 再用点号访问 —— 那样做等于
// 把"Go 的自动 padding 恰好等于 MSVC ABI"当成前提, 而这正是事故前的做法。
//
// 另一个容易被忽略的好处: 偏移由编译器按**当前字长**算出来, amd64/arm64 (指针 8
// 字节) 与 386 (指针 4 字节) 天然各自得数, 不需要 #ifdef 式的两套魔数。
type ipAddrStringABI struct {
	Next      *ipAddrStringABI
	IpAddress [ipAddressStringLen]byte
	IpMask    [ipAddressStringLen]byte
	Context   uint32
}

// ipAdapterInfoABI 对应 IP_ADAPTER_INFO (iptypes.h)。字段顺序与类型逐条照抄。
//
// HaveWins 用 int32: MSVC 的 BOOL 是 int (4 字节)。golang.org/x/sys/windows 这里
// 写的是 bool (Go 的 bool 是 1 字节), 两条路径会算出不同的 sizeof —— 我们读到的
// 字段全在 HaveWins **之前**, 所以无论哪种解释都不影响结论; 记录跨度的判据也据此
// 只用 ipAdapterInfoReadSpan 而不是 sizeof (见下)。
type ipAdapterInfoABI struct {
	Next                *ipAdapterInfoABI
	ComboIndex          uint32
	AdapterName         [maxAdapterNameLength + 4]byte
	Description         [maxAdapterDescriptionLength + 4]byte
	AddressLength       uint32
	Address             [maxAdapterAddressLength]byte
	Index               uint32
	Type                uint32
	DhcpEnabled         uint32
	CurrentIpAddress    *ipAddrStringABI
	IpAddressList       ipAddrStringABI
	GatewayList         ipAddrStringABI
	DhcpServer          ipAddrStringABI
	HaveWins            int32
	PrimaryWinsServer   ipAddrStringABI
	SecondaryWinsServer ipAddrStringABI
	LeaseObtained       int64
	LeaseExpires        int64
}

// ptrSize 是指针宽度的编译期常量表达式: 64 位得 8, 32 位得 4。
const ptrSize = 4 << (^uintptr(0) >> 63)

// 逐字段偏移。取值来自 unsafe.Offsetof, 不再有人手写数字 —— 上一次手写的结果是
// DhcpEnabled@296 / CurrentIpAddress@304 / IpAddressList@312, 而 SDK 的真实位置
// 是 424 / 432 / 440 (64 位)。
const (
	offAdapterName   = unsafe.Offsetof(ipAdapterInfoABI{}.AdapterName)
	offDescription   = unsafe.Offsetof(ipAdapterInfoABI{}.Description)
	offAddressLength = unsafe.Offsetof(ipAdapterInfoABI{}.AddressLength)
	offAddress       = unsafe.Offsetof(ipAdapterInfoABI{}.Address)
	offAdapterIndex  = unsafe.Offsetof(ipAdapterInfoABI{}.Index)
	offAdapterType   = unsafe.Offsetof(ipAdapterInfoABI{}.Type)
	offDhcpEnabled   = unsafe.Offsetof(ipAdapterInfoABI{}.DhcpEnabled)
	offCurrentIPAddr = unsafe.Offsetof(ipAdapterInfoABI{}.CurrentIpAddress)
	offIpAddressList = unsafe.Offsetof(ipAdapterInfoABI{}.IpAddressList)

	offAddrStringNext = 0 // Next 是 IP_ADDR_STRING 的第一个字段
	offAddrStringIP   = unsafe.Offsetof(ipAddrStringABI{}.IpAddress)
	offAddrStringMask = unsafe.Offsetof(ipAddrStringABI{}.IpMask)

	ipAddrStringSize  = unsafe.Sizeof(ipAddrStringABI{})
	ipAdapterInfoSize = unsafe.Sizeof(ipAdapterInfoABI{})

	// ipAdapterInfoReadSpan 是我们**真正会读到**的区间上限: 记录头到内嵌的
	// IpAddressList 结束。
	//
	// 用它而不是 ipAdapterInfoSize 做边界判据, 是因为尾部那几个字段 (HaveWins
	// / LeaseObtained / LeaseExpires) 的对齐在不同工具链/字长下有分歧 —— 我们不
	// 读它们, 就不该被它们的分歧连坐。少算的代价? 没有: 少算只会让判据更保守。
	ipAdapterInfoReadSpan = offIpAddressList + ipAddrStringSize
)

// ===== 布局自证 (编译期) =====
//
// 上面这些偏移算错时**没有任何运行时症状**, 症状在 Windows 上表现为段错误, 或者
// 更坏的: 不崩, 只是永远报"未连接"。靠注释约束不住, 所以把 SDK 的期望值钉成编译
// 期断言:
//
//	先把两个字长的期望值用 w64/w32 (0/1 开关) **折叠成一个**常量, 再断言;
//	"-(d*d)" 形式的数组长度在两值相等时为 0 (合法), 不等时为负 (**非法**), 于是
//	把"必须相等"翻译成"编译不过"。
//
// 折叠这一步是必需的, 不是风格: 若给每个字长各写一条断言、靠乘 0 关闭不生效的那
// 条, 未生效的那条仍会被求值 —— 类型化常量 `offDescription - wantOffDescription64`
// 在 386 上是 -4, uintptr 装不下, go build 直接报
// "constant -4 of type uintptr overflows uintptr" (实测), 于是 386 构建被一条
// 本该不生效的断言打死。
//
// 期望值来源: iptypes.h 的结构定义按 MSVC 对齐规则展开 (并在 64 位进程上与
// 实测一致 —— AdapterName@12 / Description@272 / DhcpEnabled@424 /
// CurrentIpAddress@432 / IpAddressList@440 / 记录跨度 704)。放在这里的好处是:
// `GOOS=windows go build ./...` 就能验出布局漂移, 不需要能在 Windows 上跑测试。
const (
	w64 = ptrSize / 8   // amd64/arm64 → 1; 386 → 0
	w32 = 8/ptrSize - 1 // 386 → 1; 其余 → 0

	wantOffAdapterName64   = 12
	wantOffDescription64   = 272
	wantOffAddressLen64    = 404
	wantOffAdapterIndex64  = 416
	wantOffAdapterType64   = 420
	wantOffDhcpEnabled64   = 424
	wantOffCurrentIP64     = 432
	wantOffIpAddressList64 = 440
	wantIpAdapterInfo64    = 704
	wantIpAddrString64     = 48

	wantOffAdapterName32   = 8
	wantOffDescription32   = 268
	wantOffAddressLen32    = 400
	wantOffAdapterIndex32  = 412
	wantOffAdapterType32   = 416
	wantOffDhcpEnabled32   = 420
	wantOffCurrentIP32     = 424
	wantOffIpAddressList32 = 428
	wantIpAdapterInfo32    = 648
	wantIpAddrString32     = 40

	wantOffAdapterName   = w64*wantOffAdapterName64 + w32*wantOffAdapterName32
	wantOffDescription   = w64*wantOffDescription64 + w32*wantOffDescription32
	wantOffAddressLength = w64*wantOffAddressLen64 + w32*wantOffAddressLen32
	wantOffAdapterIndex  = w64*wantOffAdapterIndex64 + w32*wantOffAdapterIndex32
	wantOffAdapterType   = w64*wantOffAdapterType64 + w32*wantOffAdapterType32
	wantOffDhcpEnabled   = w64*wantOffDhcpEnabled64 + w32*wantOffDhcpEnabled32
	wantOffCurrentIPAddr = w64*wantOffCurrentIP64 + w32*wantOffCurrentIP32
	wantOffIpAddressList = w64*wantOffIpAddressList64 + w32*wantOffIpAddressList32
	wantIpAdapterInfo    = w64*wantIpAdapterInfo64 + w32*wantIpAdapterInfo32
	wantIpAddrString     = w64*wantIpAddrString64 + w32*wantIpAddrString32
)

var (
	_ [-(offAdapterName - wantOffAdapterName) * (offAdapterName - wantOffAdapterName)]struct{}
	_ [-(offDescription - wantOffDescription) * (offDescription - wantOffDescription)]struct{}
	_ [-(offAddressLength - wantOffAddressLength) * (offAddressLength - wantOffAddressLength)]struct{}
	_ [-(offAdapterIndex - wantOffAdapterIndex) * (offAdapterIndex - wantOffAdapterIndex)]struct{}
	_ [-(offAdapterType - wantOffAdapterType) * (offAdapterType - wantOffAdapterType)]struct{}
	_ [-(offDhcpEnabled - wantOffDhcpEnabled) * (offDhcpEnabled - wantOffDhcpEnabled)]struct{}
	_ [-(offCurrentIPAddr - wantOffCurrentIPAddr) * (offCurrentIPAddr - wantOffCurrentIPAddr)]struct{}
	_ [-(offIpAddressList - wantOffIpAddressList) * (offIpAddressList - wantOffIpAddressList)]struct{}
	_ [-(ipAdapterInfoSize - wantIpAdapterInfo) * (ipAdapterInfoSize - wantIpAdapterInfo)]struct{}
	_ [-(ipAddrStringSize - wantIpAddrString) * (ipAddrStringSize - wantIpAddrString)]struct{}

	// 读取区间必须落在一条记录之内 (少读可以, 读过头就是别人的字段)。
	_ [ipAdapterInfoSize - ipAdapterInfoReadSpan]struct{}
)

// ===== 遍历上限 =====
//
// 污染/自引用的 Next 链不能让我们死循环。上限按"一块缓冲里塞得下的记录数"给,
// 远超任何真实机器的网卡数量 (配合下面的缓冲区上限), 够用且不至于卡住。
const (
	maxAdapterRecords = 64
	maxIPAddrHops     = 8

	// maxAdapterBufferSize 是给 GetAdaptersInfo 分配缓冲的上限 (1 MiB)。
	// 回填需求超过它 => 要么是恶意数据, 要么是我们自己的尺寸探测出了问题; 宁可
	// 报"未连接"也不要照着一个可信度不明的数字去分配。
	maxAdapterBufferSize = 1 << 20
)

// ===== 缓冲读取: 所有读取都先看边界 =====

// ipBuf 是 GetAdaptersInfo 回填缓冲的只读游标。
//
// **每个访问器都先算边界**: 这里读的是 C 结构的原始字节, 任何"按布局算出来的
// 位置"在使用前都必须先证明它落在缓冲里。越界返回零值 (而不是 panic) 是刻意的 ——
// 这份代码的失败模式必须是"降级成未连接", 不是"整个进程崩掉"。
type ipBuf struct {
	b []byte
}

func (r ipBuf) size() uintptr { return uintptr(len(r.b)) }

// base 是缓冲首字节的地址。Next/Chain 指针是**绝对地址**, 需要它换算成缓冲内的
// 相对偏移。
func (r ipBuf) base() uintptr {
	if len(r.b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&r.b[0]))
}

// has 判断 [off, off+n) 是否完整落在缓冲内。
//
// ⚠️ 写成 `r.size()-off >= n` 而不是 `off+n <= r.size()`: off 可能是**不可信**的
// Next 指针换算出来的相对偏移 (接近 2^64), 而 `off+n` 在无符号下会**回绕**成一个
// 很小的数, 于是越界读取反而通过了边界检查 —— 这条路径上就是段错误 / panic。
// (由 TestNetworkTypeFromBufferUntrustedNext 实测抓到: 合成一个"指向缓冲之前"的
// Next 直接把测试打挂。)
func (r ipBuf) has(off, n uintptr) bool {
	return off <= r.size() && r.size()-off >= n
}

// u32 读一个小端 uint32。
func (r ipBuf) u32(off uintptr) uint32 {
	if !r.has(off, 4) {
		return 0
	}
	return uint32(r.b[off]) | uint32(r.b[off+1])<<8 |
		uint32(r.b[off+2])<<16 | uint32(r.b[off+3])<<24
}

// ptr 读一个**指针宽度**的小端整数 (不是 8 字节定长): 386 上把它当 8 字节读会
// 读到下一个字段的头 4 个字节。
func (r ipBuf) ptr(off uintptr) uintptr {
	if !r.has(off, ptrSize) {
		return 0
	}
	var v uintptr
	for i := uintptr(0); i < ptrSize; i++ {
		v |= uintptr(r.b[off+i]) << (8 * i)
	}
	return v
}

// cstr 读 n 字节内的 NUL 结尾 ASCII 串 (IP_ADDRESS_STRING / 网卡名都是 ASCII)。
func (r ipBuf) cstr(off uintptr, n uintptr) string {
	if n == 0 || !r.has(off, n) {
		return ""
	}
	s := r.b[off : off+n]
	i := 0
	for i < len(s) && s[i] != 0 {
		i++
	}
	return string(s[:i])
}

// looksLikeIPv4 判断读出来的串**像不像**点分十进制 IPv4。
//
// 这是一张安全网: 万一偏移将来再次错位, 读到的是 Description 里的普通字节而不是
// IP —— 有了这道校验, 症状是"报未连接"(可诊断、只影响一个能力), 而不是"谎报已
// 连接"。判据刻意宽松 (只要求形如 a.b.c.d 且每段是 ≤255 的十进制), 合法 IP 一定
// 通过, 不会误伤。
func looksLikeIPv4(s string) bool {
	if strings.Count(s, ".") != 3 {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" || len(seg) > 3 {
			return false
		}
		n := 0
		for i := 0; i < len(seg); i++ {
			if seg[i] < '0' || seg[i] > '9' {
				return false
			}
			n = n*10 + int(seg[i]-'0')
		}
		if n > 255 {
			return false
		}
	}
	return true
}

// hasIPv4 走这条记录的 IP_ADDR_STRING 链, 任一地址串像 IPv4 且不是 0.0.0.0 即为
// "这块网卡有地址"。
//
// 注意 Next 的处理: 我们**从不 dereference 它**, 只把它换算成缓冲内的相对偏移再
// 校验。这是本文件最重要的一行性质 —— 旧实现正是把一个错位处读出的整数当指针解
// 引用, 于是 constant 0x322b 那样的地址直接把进程打下来。
func (r ipBuf) hasIPv4(recOff uintptr) bool {
	off := recOff + offIpAddressList
	for hop := 0; hop < maxIPAddrHops; hop++ {
		if !r.has(off, ipAddrStringSize) {
			return false // 这条链节点不完整: 不再往下读
		}
		if s := r.cstr(off+offAddrStringIP, ipAddressStringLen); looksLikeIPv4(s) && s != "0.0.0.0" {
			return true
		}
		next := r.ptr(off + offAddrStringNext)
		if next == 0 {
			return false
		}
		// next 小于 base 时这里回绕成巨大的正数, 被下面这条判句一并挡掉
		// (用 has 而不是加法比较, 否则回绕会骗过边界检查)。
		delta := next - r.base()
		if !r.has(delta, ipAddrStringSize) {
			return false
		}
		off = delta
	}
	return false
}

// ===== 解析: 记录链 → (有没有连上, 什么类型) =====

// IF_TYPE_* (ipifcons.h)。
const (
	ifTypeEthernetCSMACD = 6
	ifTypePPP            = 23
	ifTypeIEEE80211      = 71
)

// networkTypeFromBuffer 从记录链里判"有没有连上 + 什么类型"。
//
// 判据 (沿用既有口径, 只是把边界换成硬上限):
//
//	connected —— 任一网卡分配了**有效 IPv4 地址** (形如 IP 且非 0.0.0.0)。为什么
//	             不用 OperStatus: IP_ADAPTER_INFO **没有**这个字段 (那是更重的
//	             IP_ADAPTER_ADDRESSES 才有的), "有地址"是纯 syscall 下判断"这块
//	             网卡能用"最可靠且跨版本的信号。
//	type      —— 按 Type 字段: 71 → wifi, 6 → ethernet, 23 → vpn, 其余 → other;
//	             多张网卡同时有地址时取 wifi > ethernet > vpn > other。
//
// buf 必须已按内核回填的 size 截断 (见 queryAdapterInfo); 这里再校一次下界, 因为
// 它是唯一能触达 C 结构体的解码路径, 重复一次成本为零。
func networkTypeFromBuffer(buf []byte) (connected bool, typ string) {
	r := ipBuf{b: buf}
	// size 的健全性: 连一条记录的上限都不到 => 没有可解码的数据。
	if r.size() < ipAdapterInfoReadSpan {
		return false, "none"
	}
	hasWifi, hasEth, hasPPP := false, false, false
	for off, hop := uintptr(0), 0; hop < maxAdapterRecords; hop++ {
		// 记录必须**完整**落在 size 之内才读 —— 变长结构的最后一条常常是不完整的。
		if !r.has(off, ipAdapterInfoReadSpan) {
			break
		}
		if r.hasIPv4(off) {
			connected = true
			switch r.u32(off + offAdapterType) {
			case ifTypeIEEE80211:
				hasWifi = true
			case ifTypeEthernetCSMACD:
				hasEth = true
			case ifTypePPP:
				hasPPP = true
			}
		}
		next := r.ptr(off)
		if next == 0 {
			break
		}
		// 与上一条同律: 只换算, 不解引用; 指向 size 之外的链一律不信。
		delta := next - r.base()
		if !r.has(delta, ipAdapterInfoReadSpan) {
			break
		}
		off = delta
	}
	if !connected {
		return false, "none"
	}
	switch {
	case hasWifi:
		return true, "wifi"
	case hasEth:
		return true, "ethernet"
	case hasPPP:
		return true, "vpn"
	default:
		return true, "other"
	}
}

// queryAdapterInfo (GetAdaptersInfo 的调用) 与 hostNetworkType 在
// ipadapter_windows.go —— 只有它们需要 Win32 符号。
