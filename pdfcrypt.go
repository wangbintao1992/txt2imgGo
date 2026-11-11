package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// PDFSecurity 描述 PDF 的标准安全处理器（Standard Security Handler）设置。
//
// PDF 的权限由 32 位整数 /P 表示，标准只提供“允许”位，这里用布尔值表达允许项，
// 由 pdfPermissions 转成 /P。注意：PDF 标准把“复制文字”和“提取图片”合并为同一个
// 权限位（bit 5），因此这两项无法分别放开，任一项禁止都会同时限制另一项。
type PDFSecurity struct {
	OpenPassword  string // 打开密码（用户密码），留空表示打开时不需要密码
	OwnerPassword string // 权限密码（所有者密码），用于修改权限；留空时回退为打开密码
	AllowPrint    bool
	AllowCopy     bool
	AllowExtract  bool
}

// Enabled 表示是否需要加密。只有设置密码时才需要加密；仅勾选权限项无法单独生效。
func (s PDFSecurity) Enabled() bool {
	return s.OpenPassword != "" || s.OwnerPassword != ""
}

// Restricted 表示是否勾选了任何禁止项。
func (s PDFSecurity) Restricted() bool {
	return !s.AllowPrint || !s.AllowCopy || !s.AllowExtract
}

var pdfPasswordPad = [32]byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41,
	0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

type pdfEncryptor struct {
	key []byte // 文件密钥，16 字节
	o   []byte // /O，32 字节
	u   []byte // /U，32 字节
	p   int32  // /P
	id  []byte // /ID[0]，16 字节
}

func newPDFEncryptor(sec PDFSecurity) (*pdfEncryptor, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, fmt.Errorf("生成 PDF 加密标识失败: %w", err)
	}
	return newPDFEncryptorWithID(sec, id)
}

// newPDFEncryptorWithID 允许测试注入固定 ID，便于校验加密结果。
func newPDFEncryptorWithID(sec PDFSecurity, id []byte) (*pdfEncryptor, error) {
	if !sec.Enabled() {
		return nil, nil
	}
	if len(id) != 16 {
		return nil, fmt.Errorf("PDF 加密标识必须为 16 字节")
	}
	p := pdfPermissions(sec)
	o := pdfOwnerEntry(sec.OwnerPassword, sec.OpenPassword)
	key := pdfFileKey(sec.OpenPassword, o, p, id)
	u := pdfUserEntry(key, id)
	return &pdfEncryptor{key: key, o: o, u: u, p: p, id: id}, nil
}

// pdfPermissions 依据允许项生成 /P。
// 采用 R3 规则：bit 1-2 为 0，bit 7-8 为 1，bit 13-32 为 1；
// 默认清除 bit 3-6 与 bit 9-12，再按允许项置位。
func pdfPermissions(sec PDFSecurity) int32 {
	var p uint32 = 0xFFFFF0C0
	if sec.AllowPrint {
		p |= 1 << 2  // bit 3：打印
		p |= 1 << 11 // bit 12：高质量打印
	}
	if sec.AllowCopy && sec.AllowExtract {
		p |= 1 << 4 // bit 5：复制 / 提取文字与图形
		p |= 1 << 9 // bit 10：为无障碍提取
	}
	return int32(p)
}

func pdfPadPassword(pw string) []byte {
	out := make([]byte, 32)
	n := copy(out, pw)
	copy(out[n:], pdfPasswordPad[:])
	return out
}

func pdfOwnerEntry(ownerPW, userPW string) []byte {
	if ownerPW == "" {
		ownerPW = userPW
	}
	sum := md5.Sum(pdfPadPassword(ownerPW))
	key := append([]byte(nil), sum[:]...)
	for i := 0; i < 50; i++ {
		s := md5.Sum(key)
		key = append([]byte(nil), s[:]...)
	}

	out := make([]byte, 32)
	pdfRC4(key, pdfPadPassword(userPW), out)
	for i := 1; i <= 19; i++ {
		next := make([]byte, 32)
		pdfRC4(pdfXORKey(key, byte(i)), out, next)
		out = next
	}
	return out
}

func pdfFileKey(userPW string, o []byte, p int32, id []byte) []byte {
	h := md5.New()
	_, _ = h.Write(pdfPadPassword(userPW))
	_, _ = h.Write(o)
	var pb [4]byte
	binary.LittleEndian.PutUint32(pb[:], uint32(p))
	_, _ = h.Write(pb[:])
	_, _ = h.Write(id)
	key := h.Sum(nil)

	for i := 0; i < 50; i++ {
		s := md5.Sum(key)
		key = append([]byte(nil), s[:]...)
	}
	return key
}

func pdfUserEntry(key, id []byte) []byte {
	h := md5.New()
	_, _ = h.Write(pdfPasswordPad[:])
	_, _ = h.Write(id)
	sum := h.Sum(nil)

	out := make([]byte, 16)
	pdfRC4(key, sum, out)
	for i := 1; i <= 19; i++ {
		next := make([]byte, 16)
		pdfRC4(pdfXORKey(key, byte(i)), out, next)
		out = next
	}
	// R3 下 /U 固定 32 字节，后 16 字节为任意填充。
	res := make([]byte, 32)
	copy(res, out)
	return res
}

func pdfXORKey(key []byte, b byte) []byte {
	out := make([]byte, len(key))
	for i, k := range key {
		out[i] = k ^ b
	}
	return out
}

func pdfRC4(key, in, out []byte) {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic("pdf: invalid RC4 key: " + err.Error())
	}
	c.XORKeyStream(out, in)
}

// objectKey 计算单个对象的 RC4 密钥：MD5(fileKey + 对象号 + 代号)，长度 min(n+5, 16)。
func (e *pdfEncryptor) objectKey(num, gen int) []byte {
	h := md5.New()
	_, _ = h.Write(e.key)
	var b [5]byte
	b[0] = byte(num)
	b[1] = byte(num >> 8)
	b[2] = byte(num >> 16)
	b[3] = byte(gen)
	b[4] = byte(gen >> 8)
	_, _ = h.Write(b[:])
	sum := h.Sum(nil)

	n := len(e.key) + 5
	if n > 16 {
		n = 16
	}
	return sum[:n]
}

// Encrypt 加密对象流内容，返回与输入等长的新切片。
func (e *pdfEncryptor) Encrypt(num, gen int, data []byte) []byte {
	out := make([]byte, len(data))
	pdfRC4(e.objectKey(num, gen), data, out)
	return out
}

func (e *pdfEncryptor) DictBody() string {
	return fmt.Sprintf(
		"<< /Filter /Standard /V 2 /R 3 /Length 128 /P %d /O <%s> /U <%s> >>\n",
		e.p, hex.EncodeToString(e.o), hex.EncodeToString(e.u))
}

func (e *pdfEncryptor) IDHex() string {
	return hex.EncodeToString(e.id)
}
