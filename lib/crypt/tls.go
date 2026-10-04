package crypt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/astaxie/beego/logs"
)

var (
	cert            tls.Certificate
	certFingerprint string // 桥接证书 SHA-256 hex（F2-2）
	clientFP        string // 客户端期望的服务端指纹；空 = 沿用 InsecureSkipVerify + 告警
)

// InitTls 初始化桥接证书：优先加载 certPath/keyPath，不存在则生成 ECDSA P-256 自签证书并落盘（0600）。
// 证书路径必须与面板 HTTPS 证书（conf/server.pem|key）隔离，否则面板换证书会导致桥接指纹变化、
// 已配置指纹的客户端集体掉线（F2-2 / 定稿 v3 D4）。
func InitTls(certPath, keyPath string) error {
	if certPath != "" && keyPath != "" {
		if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
			return setCert(c)
		}
	}
	c, k, err := generateKeyPair("NPS Bridge")
	if err != nil {
		return err
	}
	cc, err := tls.X509KeyPair(c, k)
	if err != nil {
		return err
	}
	if certPath != "" && keyPath != "" {
		if err := os.MkdirAll(dirOf(certPath), 0755); err != nil {
			return err
		}
		if err := writeFile0600(certPath, c); err != nil {
			return err
		}
		if err := writeFile0600(keyPath, k); err != nil {
			return err
		}
		logs.Info("生成桥接证书并持久化: %s / %s", certPath, keyPath)
	}
	return setCert(cc)
}

func setCert(c tls.Certificate) error {
	if len(c.Certificate) == 0 {
		return errors.New("empty certificate")
	}
	cert = c
	sum := sha256.Sum256(c.Certificate[0])
	certFingerprint = hex.EncodeToString(sum[:])
	return nil
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if os.IsPathSeparator(p[i]) {
			return p[:i]
		}
	}
	return "."
}

func writeFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// GetCert 返回桥接证书（供 tls.Config.Certificates 使用）
func GetCert() tls.Certificate {
	return cert
}

// GetCertFingerprint 返回桥接证书 SHA-256 指纹（hex，小写）。
// 面板在「客户端」页与一键安装命令中展示/下发该值（F2-2 分发链路）。
func GetCertFingerprint() string {
	return certFingerprint
}

// BuildTlsServerConfig 构建桥接服务端 TLS 配置（MinVersion TLS12）
func BuildTlsServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}

func NewTlsServerConn(conn net.Conn) net.Conn {
	return tls.Server(conn, BuildTlsServerConfig())
}

// NewTlsClientConn 三态校验（F2-2）：
//   - 未设置指纹：沿用 InsecureSkipVerify（仅防被动窃听），由调用方打印醒目告警
//   - 设置指纹：InsecureSkipVerify + VerifyPeerCertificate 严格比对 SHA-256，不匹配即握手失败
//     （指纹 pin 取代链校验：自签证书无法走 CA 链，故关闭默认校验并用 pin 兜底）
func NewTlsClientConn(conn net.Conn) net.Conn {
	conf := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if clientFP == "" {
		conf.InsecureSkipVerify = true
	} else {
		conf.InsecureSkipVerify = true
		conf.VerifyPeerCertificate = pinVerifier(clientFP)
	}
	return tls.Client(conn, conf)
}

// pinVerifier 构造指纹校验器：SHA-256(证书DER) 必须等于期望值
func pinVerifier(fp string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("tls: no peer certificate")
		}
		sum := sha256.Sum256(rawCerts[0])
		if hex.EncodeToString(sum[:]) != fp {
			return fmt.Errorf("tls fingerprint mismatch: got %s, want %s", hex.EncodeToString(sum[:]), fp)
		}
		return nil
	}
}

// SetTlsFingerprint 设置客户端期望的服务端桥接证书指纹；空值 = 沿用旧行为（InsecureSkipVerify + 告警）。
func SetTlsFingerprint(fp string) {
	clientFP = fp
}

func GetTlsFingerprint() string {
	return clientFP
}

// TlsDialConfig 构建客户端 tls.Dial 配置（桥接控制连接，F2-2 三态）
func TlsDialConfig() *tls.Config {
	conf := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if clientFP == "" {
		conf.InsecureSkipVerify = true
	} else {
		conf.InsecureSkipVerify = true // 指纹 pin 取代链校验（自签证书）
		conf.VerifyPeerCertificate = pinVerifier(clientFP)
	}
	return conf
}

func generateKeyPair(CommonName string) (rawCert, rawKey []byte, err error) {
	// ECDSA P-256 自签证书（比原 RSA-2048 更小更快，安全性等价）
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	validFor := time.Hour * 24 * 365 * 10 // ten years
	notBefore := time.Now()
	notAfter := notBefore.Add(validFor)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"NekoBoxHQ"},
			CommonName:   CommonName,
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return
	}

	rawCert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return
	}
	rawKey = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return
}
