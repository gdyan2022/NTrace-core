package util

import (
	"context"
	"errors"
	"fmt"
	"github.com/nxtrace/NTrace-core/config"
	"log"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/fatih/color"
)

var DisableMPLS = GetenvDefault("NEXTTRACE_DISABLEMPLS", "")
var EnableHidDstIP = GetenvDefault("NEXTTRACE_ENABLEHIDDENDSTIP", "")
var EnvIPInfoLocalPath = GetenvDefault("NEXTTRACE_IPINFOLOCALPATH", "")
var EnvRandomPort = GetenvDefault("NEXTTRACE_RANDOMPORT", "")
var EnvToken = GetenvDefault("NEXTTRACE_TOKEN", "")
var Uninterrupted = GetenvDefault("NEXTTRACE_UNINTERRUPTED", "")
var DestIP string
var PowProviderParam = ""
var RdnsCache sync.Map
var UserAgent = fmt.Sprintf("NextTrace %s/%s/%s", config.Version, runtime.GOOS, runtime.GOARCH)
var cachedLocalIP net.IP
var cachedLocalPort int
var localIPOnce sync.Once
var cachedLocalIPv6 net.IP
var cachedLocalPort6 int
var localIPv6Once sync.Once

func LookupAddr(addr string) ([]string, error) {
	// 如果在缓存中找到，直接返回
	if hostname, ok := RdnsCache.Load(addr); ok {
		//fmt.Println("hit RdnsCache for", addr, hostname)
		return []string{hostname.(string)}, nil
	}
	// 如果缓存中未找到，进行 DNS 查询
	names, err := net.LookupAddr(addr)
	if err != nil {
		return nil, err
	}
	// 将查询结果存入缓存
	if len(names) > 0 {
		RdnsCache.Store(addr, names[0])
	}
	return names, nil
}

// getLocalIPPort encapsulates the logic to get local IP and port via a UDP connection
func getLocalIPPort(dstip net.IP) (net.IP, int) {
	serverAddr, err := net.ResolveUDPAddr("udp", dstip.String()+":12345")
	if err != nil {
		log.Fatal(err)
	}
	con, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return nil, -1
	}
	defer con.Close()
	if udpaddr, ok := con.LocalAddr().(*net.UDPAddr); ok {
		return udpaddr.IP, udpaddr.Port
	}
	return nil, -1
}

// getLocalIPPortv6 encapsulates the logic to get local IPv6 and port via a UDP connection
func getLocalIPPortv6(dstip net.IP) (net.IP, int) {
	serverAddr, err := net.ResolveUDPAddr("udp", "["+dstip.String()+"]:12345")
	if err != nil {
		log.Fatal(err)
	}
	con, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return nil, -1
	}
	defer con.Close()
	if udpaddr, ok := con.LocalAddr().(*net.UDPAddr); ok {
		return udpaddr.IP, udpaddr.Port
	}
	return nil, -1
}

// LocalIPPort returns the local IP and port based on our destination IP, with caching unless EnvRandomPort is set.
func LocalIPPort(dstip net.IP) (net.IP, int) {
	// If EnvRandomPort is set, bypass caching and return a new port every time.
	if EnvRandomPort != "" {
		return getLocalIPPort(dstip)
	}

	// Otherwise, use the cached value (computed only once).
	localIPOnce.Do(func() {
		cachedLocalIP, cachedLocalPort = getLocalIPPort(dstip)
	})
	if cachedLocalIP != nil {
		return cachedLocalIP, cachedLocalPort
	}
	return nil, -1
}

func LocalIPPortv6(dstip net.IP) (net.IP, int) {
	// If EnvRandomPort is set, bypass caching and return a new port every time.
	if EnvRandomPort != "" {
		return getLocalIPPortv6(dstip)
	}

	// Otherwise, use the cached value (computed only once).
	localIPv6Once.Do(func() {
		cachedLocalIPv6, cachedLocalPort6 = getLocalIPPortv6(dstip)
	})
	if cachedLocalIPv6 != nil {
		return cachedLocalIPv6, cachedLocalPort6
	}
	return nil, -1
}

func DomainLookUp(host string, ipVersion string, dotServer string, disableOutput bool) (net.IP, error) {
	// ipVersion: 4, 6, all
	var (
		r   *net.Resolver
		ips []net.IP
	)

	switch dotServer {
	case "dnssb":
		r = DNSSB()
	case "aliyun":
		r = Aliyun()
	case "dnspod":
		r = Dnspod()
	case "google":
		r = Google()
	case "cloudflare":
		r = Cloudflare()
	default:
		r = newUDPResolver()
	}
	ipsStr, err := r.LookupHost(context.Background(), host)
	for _, v := range ipsStr {
		ips = append(ips, net.ParseIP(v))
	}
	if err != nil {
		return nil, errors.New("DNS lookup failed")
	}

	//var ipv6Flag = false
	//TODO: 此处代码暂无意义
	//if ipv6Flag {
	//	fmt.Println("[Info] IPv6 UDP Traceroute is not supported right now.")
	//	if len(ips) == 0 {
	//		os.Exit(0)
	//	}
	//}

	// Filter by IPv4/IPv6
	if ipVersion != "all" {
		var filteredIPs []net.IP
		for _, ip := range ips {
			if ipVersion == "4" && ip.To4() != nil {
				filteredIPs = []net.IP{ip}
				break
			} else if ipVersion == "6" && strings.Contains(ip.String(), ":") {
				filteredIPs = []net.IP{ip}
				break
			}
		}
		ips = filteredIPs
	}

	if (len(ips) == 1) || (disableOutput) {
		return ips[0], nil
	} else {
		fmt.Println("Please Choose the IP You Want To TraceRoute")
		for i, ip := range ips {
			fmt.Fprintf(color.Output, "%s %s\n",
				color.New(color.FgHiYellow, color.Bold).Sprintf("%d.", i),
				color.New(color.FgWhite, color.Bold).Sprintf("%s", ip),
			)
		}
		var index int
		fmt.Printf("Your Option: ")
		_, err := fmt.Scanln(&index)
		if err != nil {
			index = 0
		}
		if index >= len(ips) || index < 0 {
			fmt.Println("Your Option is invalid")
			os.Exit(3)
		}
		return ips[index], nil
	}
}

func GetenvDefault(key, defVal string) string {
	val, ok := os.LookupEnv(key)
	if ok {
		_, ok := os.LookupEnv("NEXTTRACE_DEBUG")
		if ok {
			fmt.Println("ENV", key, "detected as", val)
		}
		return val
	}
	return defVal
}

func GetHostAndPort() (host string, port string) {
	var hostP = GetenvDefault("NEXTTRACE_HOSTPORT", "origin-fallback.nxtrace.org")
	// 解析域名
	hostArr := strings.Split(hostP, ":")
	// 判断是否有指定端口
	if len(hostArr) > 1 {
		// 判断是否为 IPv6
		if strings.HasPrefix(hostP, "[") {
			tmp := strings.Split(hostP, "]")
			host = tmp[0]
			host = host[1:]
			if port = tmp[1]; port != "" {
				port = port[1:]
			}
		} else {
			host, port = hostArr[0], hostArr[1]
		}
	} else {
		host = hostP
	}
	if port == "" {
		// 默认端口
		port = "443"
	}
	return
}

func GetProxy() *url.URL {
	proxyURLStr := GetenvDefault("NEXTTRACE_PROXY", "")
	if proxyURLStr == "" {
		return nil
	}
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		log.Println("Failed to parse proxy URL:", err)
		return nil
	}
	return proxyURL
}

func GetPowProvider() string {
	var powProvider string
	if PowProviderParam == "" {
		powProvider = GetenvDefault("NEXTTRACE_POWPROVIDER", "api.nxtrace.org")
	} else {
		powProvider = PowProviderParam
	}
	if powProvider == "sakura" {
		return "pow.nexttrace.owo.13a.com"
	}
	return ""
}

func StringInSlice(val string, list []string) bool {
	for _, v := range list {
		if v == val {
			return true
		}
	}
	return false
}

func HideIPPart(ip string) string {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ""
	}

	if parsedIP.To4() != nil {
		// IPv4: 隐藏后16位
		return strings.Join(strings.Split(ip, ".")[:2], ".") + ".0.0/16"
	}
	// IPv6: 隐藏后96位
	return parsedIP.Mask(net.CIDRMask(32, 128)).String() + "/32"
}
