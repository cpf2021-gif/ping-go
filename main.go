package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"regexp"
	"time"
)

const (
	MAX_DATA = 1392
)

var (
	timeout int64  = 1000 // 超时时间
	size    int           // 发送的数据包的大小
	count   int           // 发送请求的次数
	isloop  *bool         // 无限请求
	srcaddr string        // 发送的源地址
)

var (
	numPack  int               // 发送总数
	dropPack int               // 丢失总数
	max_lan  float64   = 0     // 最大延迟
	min_lan  float64   = 10000 // 最小延迟
	ret_list []float64         // 延迟列表
)

var (
	originBytes []byte
)

var (
	ipHeader IP = IP{
		Version:  69,
		TOS:      0,
		Flags:    0,
		TTL:      56,
		Protocol: 1,
	}
)

func init() {
	originBytes = make([]byte, MAX_DATA)
	for i := 0; i < MAX_DATA; i++ {
		originBytes[i] = byte(0)
	}
}

// 查询报文
type ICMP struct {
	Type        uint8  // 8请求 0应答
	Code        uint8  // echo 0
	Checksum    uint16 // 校验和
	Identifier  uint16 // 标识符
	SequenceNum uint16 // 序列号
}

// IP 首部
type IP struct {
	Version uint8 // 版本和首部长度  4(0100) + 20(0101) 01000101

	TOS uint8 // 服务类型

	TotalLen   uint16 // 总长度
	Identifier uint16 // 标识符

	Flags uint16 // 标志和片偏移

	TTL      uint8  // 生存时间
	Protocol uint8  // 协议
	Checksum uint16 // 校验和
	SrcAddr  uint32 // 源地址
	DstAddr  uint32 // 目的地址
}

func IpStringToUint32(ipstr string) uint32 {
	ip := net.ParseIP(ipstr)

	ip = ip.To4()

	ipUint32 := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	return ipUint32
}

func CheckSum(data []byte) uint16 {
	var (
		sum    uint32
		length int = len(data)
		index  int
	)

	// 按照每两个字节一组进行计算
	for length > 1 {
		sum += uint32(data[index])<<8 + uint32(data[index+1])
		index += 2
		length -= 2
	}

	// 如果是奇数个字节，就把最后一个字节单独计算
	if length > 0 {
		sum += uint32(data[index])
	}

	sum += (sum >> 16)
	return uint16(^sum)
}

func GetCommandArgs() {
	flag.IntVar(&size, "l", 56, "Size")
	flag.IntVar(&count, "n", 4, "Count")
	flag.StringVar(&srcaddr, "S", "172.17.0.1", "Srcaddr")
	isloop = flag.Bool("t", false, "Loop")
	flag.Parse()
}

func PrintResult() {
	fmt.Printf("数据包: 已发送 = %d, 已接收 = %d  丢包率: %.2f %% \n", int(numPack), (int(numPack) - int(dropPack)), float64(dropPack)/float64(numPack)*100)
	if len(ret_list) == 0 {
		fmt.Println("没有收到任何回复...")
	} else {
		sum := 0.0
		for _, n := range ret_list {
			sum += n
		}
		avg_lan := sum / float64((numPack - dropPack))
		fmt.Printf("rtt 最短 = %.3fms 平均 = %.3fms 最长 = %.3fms\n", min_lan, avg_lan, max_lan)
	}
}

func OneLoop(conn *net.IPConn, seq uint16) {
	if err := PingLoop(conn, uint16(numPack)); err != nil {
		numPack++
		dropPack++
	} else {
		numPack++
	}	
}

func PingLoop(conn *net.IPConn, seq uint16) error {
	// 初始化 ICMP
	icmp := ICMP{
		Type:        8,
		Code:        0,
		Checksum:    0,
		Identifier:  seq,
		SequenceNum: seq,
	}

	ipHeader.TotalLen = uint16(20) + uint16(size+8)
	ipHeader.SrcAddr = IpStringToUint32(conn.LocalAddr().String())
	ipHeader.DstAddr = IpStringToUint32(conn.RemoteAddr().String())
	ipHeader.Identifier = seq

	// 序列化 IP
	var ipBuffer bytes.Buffer

	binary.Write(&ipBuffer, binary.BigEndian, ipHeader)

	// 计算校验和
	bb := ipBuffer.Bytes()
	binary.BigEndian.PutUint16(bb[10:12], CheckSum(bb))

	// 序列化 ICMP
	var icmpBuffer bytes.Buffer

	binary.Write(&icmpBuffer, binary.BigEndian, icmp)
	binary.Write(&icmpBuffer, binary.BigEndian, originBytes[:size])

	// 计算校验和(icmp)
	b := icmpBuffer.Bytes()
	binary.BigEndian.PutUint16(b[2:4], CheckSum(b))

	recv := make([]byte, 2048)

	// 封装了底层的 IP 协议细节，并根据目标 IP 地址自动添加 IP 首部
	if _, err := conn.Write(icmpBuffer.Bytes()); err != nil {
		fmt.Println("发送失败, ", err)
		return err
	}

	t_start := time.Now()
	// 设置超时
	conn.SetReadDeadline((time.Now().Add(time.Duration(timeout) * time.Millisecond)))
	len, err := conn.Read(recv)
	if err != nil {
		fmt.Println("接收失败, ", err)
		return err
	}

	t_end := time.Now()
	dur := float64(t_end.Sub(t_start).Nanoseconds()) / 1e6
	ret_list = append(ret_list, dur)
	if dur > max_lan {
		max_lan = dur
	}
	if dur < min_lan {
		min_lan = dur
	}

	fmt.Printf("来自 %s 的回复: 字节=%d 时间=%.3fms\n", conn.RemoteAddr().String(), len, dur)
	return nil
}

func Ping(url string) {
	// 解析域名
	var ip net.IP

	if srcaddr != "" {
		ip = net.ParseIP(srcaddr)
		if ip == nil {
			log.Fatal("无效的IP地址")
		}
	}

	var raddr, err = net.ResolveIPAddr("ip4", url)

	if err != nil {
		log.Fatal("域名解析发生错误/无效的IP地址")
	}

	// 创建原始的IP数据报连接
	var conn *net.IPConn
	laddr := net.IPAddr{IP: ip}
	conn, err = net.DialIP("ip4:icmp", &laddr, raddr)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("[%s] 正在 Ping %s [%s] 具有 %d(%d) 字节的数据:\n", srcaddr, url, raddr.String(), size, size+20+8)

	defer conn.Close()

	if *isloop {
		for {
			OneLoop(conn, uint16(numPack))
			time.Sleep(1000 * time.Millisecond)
		}
	} else {
		for count > 0 {
			OneLoop(conn, uint16(numPack))
			count--
			time.Sleep(1000 * time.Millisecond)
		}
	}
}

func main() {
	// 创建一个用于接收中断信号的通道
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt)

	// 创建一个用于结束程序的通道
	doneChan := make(chan struct{})

	// 监听中断信号
	go func() {
		<-interruptChan
		PrintResult()
		close(doneChan)
	}()

	// 主体
	go func() {
		// 不能同时 -n 和 -t
		GetCommandArgs()

		if count != 4 && *isloop {
			log.Fatal("参数错误, -n 和 -t 不能同时使用")
		}

		// 判断是否有无效参数
		if timeout < 0 || size < 0 || count < 0 {
			log.Fatal("参数设置错误")
		}

		// size < Max_DATA
		if size > MAX_DATA {
			log.Fatalf("数据字段的字节数不能超过%d", MAX_DATA)
		}

		// 功能说明
		if len(os.Args) < 2 {
			fmt.Println("Usage: goping [-w timeout] [-l bytes] [-n count] [-t] domain")
			fmt.Println("Options:")
			fmt.Println("  -l bytes      指定发送的字节数")
			fmt.Println("  -n count      指定要发送的回显请求数")
			fmt.Println("  -t            无限循环发送请求，直到手动停止")
			fmt.Println("  -S srcaddr    指定原地址")
			close(doneChan)
			return
		}

		url := os.Args[len(os.Args)-1]
		//www.xx.xx
		pattern1 := `^www(\.[a-zA-Z0-9\-]+){2}$`
		regexpObj1, _ := regexp.Compile(pattern1)

		// ipv4
		pattern2 := `^(\d{1,3}\.){3}\d{1,3}$`
		regexpObj2, _ := regexp.Compile(pattern2)

		if !regexpObj1.MatchString(url) && !regexpObj2.MatchString(url) {
			log.Fatal("错误的域名(ip)参数/无域名(ip)参数")
		}

		Ping(url)
		PrintResult()
		close(doneChan)
	}()

	<-doneChan
}
