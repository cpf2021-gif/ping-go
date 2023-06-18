package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

const (
	MAX_DATA = 2000
)

var (
	timeout int64
	size    int
	count   int
	isloop  *bool
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

func init() {
	originBytes = make([]byte, MAX_DATA)
	for i := 0; i < MAX_DATA; i++ {
		originBytes[i] = byte(i)
	}
}

type ICMP struct {
	Type        uint8  // 8请求 0应答
	Code        uint8  // echo 0
	Checksum    uint16 // 校验和
	Identifier  uint16 // 标识符
	SequenceNum uint16 // 序列号
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
	flag.Int64Var(&timeout, "w", 1000, "Timeout")
	flag.IntVar(&size, "s", 56, "Size")
	flag.IntVar(&count, "n", 4, "Count")
	isloop = flag.Bool("t", false, "Loop")
	flag.Parse()
}

func PrintResult() {
	fmt.Printf("数据包: 已发送 = %d, 已接收 = %d  丢包率: %.2f%%\n", int(numPack), (int(numPack) - int(dropPack)), float64(dropPack)/float64(numPack)*100)
	if len(ret_list) == 0 {
		fmt.Printf("没有收到任何回复...")
	} else {
		sum := 0.0
		for _, n := range ret_list {
			sum += n
		}
		avg_lan := sum / float64((numPack - dropPack))
		fmt.Printf("rtt 最短 = %.3fms 平均 = %.3fms 最长 = %.3fms\n", min_lan, avg_lan, max_lan)
	}
}

func PingLoop(conn *net.IPConn, raddr *net.IPAddr, seq uint16) error {
	// 初始化 ICMP
	icmp := ICMP{
		Type:        8,
		Code:        0,
		Checksum:    0,
		Identifier:  0,
		SequenceNum: seq,
	}

	// 序列化 ICMP
	var buffer bytes.Buffer
	binary.Write(&buffer, binary.BigEndian, icmp)
	binary.Write(&buffer, binary.BigEndian, originBytes[:size])

	// 计算校验和
	b := buffer.Bytes()
	binary.BigEndian.PutUint16(b[2:4], CheckSum(b))

	recv := make([]byte, 1024)

	if _, err := conn.Write(buffer.Bytes()); err != nil {
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

	fmt.Printf("来自 %s 的回复: 字节=%d 时间=%.3fms\n", raddr.String(), len, dur)
	return nil
}

func Ping(url string) {
	// 解析域名
	var (
		laddr    = net.IPAddr{IP: net.ParseIP("0.0.0.0")}
		raddr, _ = net.ResolveIPAddr("ip", url)
	)

	// 创建连接
	conn, err := net.DialIP("ip4:icmp", &laddr, raddr)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n正在 Ping %s [%s] 具有 %d(%d) 字节的数据:\n", url, raddr.String(), size, size+8)

	defer conn.Close()

	// 循环发送
	for *isloop || count > 0 {
		if err := PingLoop(conn, raddr, uint16(numPack)); err != nil {
			numPack++
			dropPack++
		} else {
			numPack++
		}
		count--
		time.Sleep(1 * time.Second)
	}
}

func main() {
	// 不能同时 -n 和 -t
	GetCommandArgs()
	if count != 4 && *isloop {
		log.Fatal("参数错误, -n 和 -t 不能同时使用")
	}

	// 判断是否有无效参数
	if timeout < 0 || size < 0 || count < 0 {
		log.Fatal("参数设置错误")
	}

	// 功能说明
	if len(os.Args) < 2 {
		fmt.Println("Usage: goping [-w timeout] [-l bytes] [-n count] [-t] host")
		fmt.Println("Options:")
		fmt.Println("  -w timeout    指定超时时间，单位为毫秒")
		fmt.Println("  -l bytes      指定发送的字节数")
		fmt.Println("  -n count      指定要发送的回显请求数")
		fmt.Println("  -t            无限循环发送请求，直到手动停止")
		return
	}

	url := os.Args[len(os.Args)-1]
	//TODO: 判断是否是域名

	Ping(url)
	// TODO: 解决ctrl + c 时不执行的问题
	defer PrintResult()
}