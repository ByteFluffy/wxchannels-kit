// Package public /*
/*

									 Package public
------------------------------------------------------------------------------------------------
                                   程序所用到的所有公共常量
------------------------------------------------------------------------------------------------
*/
package public

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/qtgolang/SunnyNet/src/http"
	"github.com/qtgolang/SunnyNet/src/websocket"
)

const SunnyVersion = "2025-12-25"
const Information = `
------------------------------------------------------
       欢迎使用 SunnyNet 网络中间件 - V` + SunnyVersion + `   
                 本项目为开源项目  
            仅用于技术交流学习和研究的目的 
          请遵守法律法规,请勿用作任何非法用途 
               否则造成一切后果自负 
           若您下载并使用即视为您知晓并同意
------------------------------------------------------
        Sunny开源项目网站：https://esunny.vip
		 SunnyNet QQ交流群(一群)：751406884
         SunnyNet QQ交流群(二群)：545120699
         SunnyNet QQ交流群(三群)：170902713
         SunnyNet QQ交流群(四群)：1070797457
       QQ频道：https://pd.qq.com/g/SunnyNetV5
------------------------------------------------------

`

func init() {
	fmt.Println(Information)
}

// TCP请求相关
const (
	SunnyNetMsgTypeTCPConnectOK      = 0 //TCP连接成功
	SunnyNetMsgTypeTCPClientSend     = 1 //客户端发送数据
	SunnyNetMsgTypeTCPClientReceive  = 2 //客户端收到数据
	SunnyNetMsgTypeTCPClose          = 3 //连接关闭或连接失败
	SunnyNetMsgTypeTCPAboutToConnect = 4 //TCP即将开始连接
)

// UDP请求相关
const (
	SunnyNetUDPTypeClosed  = 1 //关闭
	SunnyNetUDPTypeSend    = 2 //客户端发送数据
	SunnyNetUDPTypeReceive = 3 //客户端收到数据
)

// WebSocket相关
const (
	WebsocketConnectionOK = 1 //Websocket连接成功
	WebsocketUserSend     = 2 //Websocket发送数据
	WebsocketServerSend   = 3 //Websocket收到数据
	WebsocketDisconnect   = 4 //Websocket断开
)

// http/s 相关
const (
	HttpSendRequest = 1 //http发送请求
	HttpResponseOK  = 2 //http接收完成
	HttpRequestFail = 3 //http请求失败
)
const (
	HttpRequestPrefix  = "http" + "://"
	HttpsRequestPrefix = "https://"

	HttpMethodGET               = "GET"
	HttpMethodPOST              = "POST"
	HttpMethodPUT               = "PUT"
	HttpMethodPATCH             = "PATCH"
	HttpMethodTRACE             = "TRACE"
	HttpMethodDELETE            = "DELETE"
	HttpMethodHEAD              = "HEAD"
	HttpMethodOPTIONS           = "OPTIONS"
	HttpMethodCONNECT           = "CONNECT"
	HTTP2                       = "PRI"
	TunnelConnectionEstablished = "HTTP/1.1 200 Connection Established\r\n\r\n" // 通道连接建立
	HttpResponseStatus100       = "HTTP/1.1 100 Continue\r\n\r\n"               //HTTP POST 请求 未发送Body时,回执此消息让客户端继续发送Body
	HttpDefaultPort             = "80"                                          //HTTP请求的默认端口
	HttpsDefaultPort            = "443"                                         //HTTPS请求的默认端口

	TagTcpAgreement                              = "TCP"
	TagTcpSSLAgreement                           = "TLS-TCP"
	TagMustTCP                                   = "TCP-Must"
	CertificateRequestManagerRulesSend           = 1 //指定证书使用规则,发送使用
	CertificateRequestManagerRulesSendAndReceive = 2 //指定证书使用规则,发送及解析使用
	CertificateRequestManagerRulesReceive        = 3 //指定证书使用规则,解析使用

	SunnyNetRawRequestBody       = http.SunnyNetRawRequestBody
	SunnyNetRawRequestBodyLength = http.SunnyNetRawRequestBodyLength
	SunnyNetRawBodySaveFilePath  = http.SunnyNetRawBodySaveFilePath
	Connect_Raw_Address          = "_connect_address_" //连接原始地址
	HTTPClientTags               = "SunnyNetHTTPClient"
	OutRouterIPKey               = "_OutRouterIPKey_"
	SunnyNetServerIpTags         = websocket.SunnyNetServerIpTags
)

// 用户浏览器访问 以下地址 可以下载证书(要访问以下地址用户必须设置代理)
const (
	CertDownloadHost1 = "sunny.io" //用户浏览器访问 http://sunny.io  可以下载证书(用户必须设置代理)
	CertDownloadHost2 = "1.2.3.4"  //用户浏览器访问 http://1.2.3.4   可以下载证书(用户必须设置代理)
	/*	除了以上地址外，还有软件运行时的IP地址
		访问 软件运行时的IP地址 + 软件运行时的端口
		例如: 127.0.0.1:8888
		这种方式下载证书用户不用设置代理
	*/
)

// 其他配置常量
const (
	Space       = " " //单个空格
	NULL        = ""  //空字符串
	Nulls       = "NULL"
	CRLF        = "\r\n"          //回车+换行
	WaitingTime = 3 * time.Second //请求底层TCP连接维持多少时间
)

var NULLPtr = uintptr(0) //空字符串指针

// s5 相关常量
const (
	Socks5Version  = uint8(5)
	Socks5AuthNone = uint8(0x00)

	// Socks5AuthGSSAPI       = uint8(0x01)

	Socks5Auth = uint8(0x02)
	// Socks5AuthUnAcceptable = uint8(0xFF)

	Socks5CmdConnect     = uint8(0x01)
	Socks5CmdBind        = uint8(0x02)
	Socks5CmdUDP         = uint8(0x03)
	Socks5typeIpv4       = uint8(0x01)
	Socks5typeDomainName = uint8(0x03)
	Socks5typeIpv6       = uint8(0x04)
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Sunny中间件自带默认证书
const (
	RootCa  = "" // wxchannels-kit: shared CA material intentionally removed; supply a local CA.
	RootKey = "" // wxchannels-kit: shared CA material intentionally removed; supply a local CA.
)
