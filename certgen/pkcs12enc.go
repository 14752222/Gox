// 本文件实现 PKCS#12 (PFX) 的编码器 —— golang.org/x/crypto/pkcs12 只有解码,
// 官方编码器在 software.ssl.golang.org/pkcs12（国内网络不可达, 见 2026-09-26 探针记录）,
// 所以按仓库"从零实现"的取向自己写一份。
//
// 产物格式对齐 OpenSSL 缺省输出（兼容性最广的那种）:
//   - 私钥袋: pkcs8ShroudedKeyBag, pbeWithSHAAnd3-KeyTripleDES-CBC (PKCS#12 PBE)
//   - 证书袋: certBag (x509Certificate), 明文 data safe
//   - MAC: HMAC-SHA1, PKCS#12 KDF 派生密钥
// keytool (JDK 8+) / gradle / Windows signtool / BouncyCastle (hap-sign-tool)
// 全部可读, x/crypto/pkcs12.Decode 也能解开（测试用这个做 round-trip）。
//
// 实现依据 RFC 7292: KDF 见附录 B.2, PFX/MacData 结构见第 4 节。
package certgen

import (
	"bytes"
	"crypto"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"fmt"
	"hash"
	"unicode/utf16"
)

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	// bagtypes 弧 = pkcs-12(1.2.840.113549.1.12) 的 10.1 子节点（RFC 7292 §4.2.2,
	// 不是 12.12.1 —— 手写时最容易记岔的地方）。
	oidPkcs8ShroudedKeyBag = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidCertBag             = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidX509Certificate     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
	oidFriendlyName        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 20}
	oidLocalKeyID          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 21}
	oidPBEWithSHAAnd3DES   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 3}
	oidSHA1                = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
)

// ---- 顶层结构（对应 RFC 7292 第 4 节）----

type pfx struct {
	Version  int
	AuthSafe contentInfo
	MacData  macData `asn1:"optional"`
}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	// data 型 ContentInfo 的 content 是 [0] EXPLICIT OCTET STRING。
	Content asn1.RawValue `asn1:"explicit,tag:0"`
}

type macData struct {
	Mac        digestInfo
	MacSalt    []byte
	Iterations int `asn1:"default:1"`
}

type digestInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	Digest    []byte
}

type safeBag struct {
	Id         asn1.ObjectIdentifier
	Value      asn1.RawValue `asn1:"explicit,tag:0"`
	Attributes []attribute   `asn1:"optional,set"`
}

type attribute struct {
	Id     asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

type encryptedPrivateKeyInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	EncData   []byte
}

// EncryptedData（PKCS#7 6 节）: version 0 + 加密内容信息。
type encryptedData struct {
	Version              int
	EncryptedContentInfo encryptedContentInfo
}

type encryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
	// [0] IMPLICIT OCTET STRING —— OpenSSL/Java 输出都是隐式原语, 别加 IsCompound
	EncryptedContent asn1.RawValue `asn1:"tag:0,optional"`
}

type pbeParams struct {
	Salt       []byte
	Iterations int
}

// encodePKCS12 把私钥 + 证书打成 PFX。key 支持 RSA/ECDSA（统一走 PKCS8）。
// alias 写进 friendlyName 属性（keytool -list / gradle 看到的就是它）。
func encodePKCS12(key crypto.PrivateKey, cert *x509.Certificate, password, alias string) ([]byte, error) {
	if password == "" {
		return nil, fmt.Errorf("pkcs12: 密码不能为空")
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	passBytes := bmpString(password)

	// 私钥袋: PKCS8 → 3DES 加密 → pkcs8ShroudedKeyBag
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	encKey, err := pbeEncrypt(oidPBEWithSHAAnd3DES, keyDER, passBytes, salt, 2048)
	if err != nil {
		return nil, err
	}
	algParams, err := asn1.Marshal(pbeParams{Salt: salt, Iterations: 2048})
	if err != nil {
		return nil, err
	}
	shrouded, err := asn1.Marshal(encryptedPrivateKeyInfo{
		Algorithm: pkix.AlgorithmIdentifier{
			Algorithm:  oidPBEWithSHAAnd3DES,
			Parameters: asn1.RawValue{FullBytes: algParams},
		},
		EncData: encKey,
	})
	if err != nil {
		return nil, err
	}

	// localKeyId: 把 key 袋和 cert 袋绑成一对（任一恒定值都行, 惯例用证书 SHA-1）
	keyID := sha1SumBytes(cert.Raw)

	// bagValue 必须是 [0] EXPLICIT —— 注意 encoding/asn1 对 RawValue 字段
	// 原样输出、忽略 struct tag, 所以 [0] 包装要在 RawValue 里手工带上。
	keyBag := safeBag{
		Id:    oidPkcs8ShroudedKeyBag,
		Value: explicitTag0(shrouded),
		Attributes: []attribute{
			{Id: oidFriendlyName, Values: []asn1.RawValue{bmpRawValue(alias)}},
			{Id: oidLocalKeyID, Values: []asn1.RawValue{octetRawValue(keyID)}},
		},
	}

	// 证书袋: bagValue = SEQUENCE { certId, [0] EXPLICIT OCTET STRING(certDER) }
	certOctet, err := asn1.Marshal(cert.Raw)
	if err != nil {
		return nil, err
	}
	certBagValue, err := asn1.Marshal([]interface{}{
		oidX509Certificate,
		explicitTag0(certOctet),
	})
	if err != nil {
		return nil, err
	}
	certBag := safeBag{
		Id:    oidCertBag,
		Value: explicitTag0(certBagValue),
		Attributes: []attribute{
			{Id: oidFriendlyName, Values: []asn1.RawValue{bmpRawValue(alias)}},
			{Id: oidLocalKeyID, Values: []asn1.RawValue{octetRawValue(keyID)}},
		},
	}

	// AuthenticatedSafe = SEQUENCE OF ContentInfo, 两项（Java/OpenSSL 同款布局,
	// x/crypto 与 JDK 的解码器都按"恰好两个"校验）:
	//   [0] data          —— 证书袋明文
	//   [1] encryptedData —— 密钥袋整体 3DES 加密
	certContents, err := asn1.Marshal([]safeBag{certBag})
	if err != nil {
		return nil, err
	}
	dataCI, err := asn1.Marshal(contentInfo{
		ContentType: oidData,
		Content:     octetExplicit(certContents),
	})
	if err != nil {
		return nil, err
	}

	// encryptedData: 加密 safeContents（SEQUENCE OF SafeBag）
	keyContents, err := asn1.Marshal([]safeBag{keyBag})
	if err != nil {
		return nil, err
	}
	encSalt := make([]byte, 8)
	if _, err := rand.Read(encSalt); err != nil {
		return nil, err
	}
	encData, err := pbeEncrypt(oidPBEWithSHAAnd3DES, keyContents, passBytes, encSalt, 2048)
	if err != nil {
		return nil, err
	}
	encParams, err := asn1.Marshal(pbeParams{Salt: encSalt, Iterations: 2048})
	if err != nil {
		return nil, err
	}
	// EncryptedContentInfo ::= SEQUENCE { contentType, alg, encryptedContent [0] IMPLICIT OCTET STRING }
	encryptedDataBytes, err := asn1.Marshal(encryptedData{
		Version: 0,
		EncryptedContentInfo: encryptedContentInfo{
			ContentType: oidData,
			ContentEncryptionAlgorithm: pkix.AlgorithmIdentifier{
				Algorithm:  oidPBEWithSHAAnd3DES,
				Parameters: asn1.RawValue{FullBytes: encParams},
			},
			// [0] IMPLICIT OCTET STRING（OpenSSL/Java 同款; x/crypto 按隐式解析）
			EncryptedContent: asn1.RawValue{Class: 2, Tag: 0, Bytes: encData},
		},
	})
	if err != nil {
		return nil, err
	}
	encCI, err := asn1.Marshal(contentInfo{
		ContentType: oidEncryptedData,
		// PKCS#7: encryptedData 型的 content 是 [0] EXPLICIT 直接包 SEQUENCE,
		// 不像 data 型还要套一层 OCTET STRING。
		Content: explicitTag0(encryptedDataBytes),
	})
	if err != nil {
		return nil, err
	}

	authSafe, err := asn1.Marshal([]asn1.RawValue{
		{FullBytes: dataCI},
		{FullBytes: encCI},
	})
	if err != nil {
		return nil, err
	}

	// MAC: HMAC-SHA1(authSafe = OCTET STRING 的值), 密钥走 PKCS#12 KDF (id=3)
	macSalt := make([]byte, 8)
	if _, err := rand.Read(macSalt); err != nil {
		return nil, err
	}
	macKey := pkcs12KDF(passBytes, macSalt, 3, 2048, sha1.Size, sha1.New)
	mac := hmac.New(sha1.New, macKey)
	mac.Write(authSafe)
	digestAlgParams, err := asn1.Marshal(asn1.NullRawValue)
	if err != nil {
		return nil, err
	}
	out, err := asn1.Marshal(pfx{
		Version: 3,
		AuthSafe: contentInfo{
			ContentType: oidData,
			Content:     octetExplicit(authSafe),
		},
		MacData: macData{
			Mac: digestInfo{
				Algorithm: pkix.AlgorithmIdentifier{
					Algorithm:  oidSHA1,
					Parameters: asn1.RawValue{FullBytes: digestAlgParams},
				},
				Digest: mac.Sum(nil),
			},
			MacSalt:    macSalt,
			Iterations: 2048,
		},
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- RFC 7292 附录 B.2 的 PKCS#12 KDF ----

// pkcs12KDF 从密码/盐派生密钥材料。id: 1=密钥, 2=IV, 3=MAC。
// SHA-1: u=20 字节, v=64 字节（RFC 表格 512 位分组派生块）。
// 注意两个易错点（都是踩过的）:
//   - 迭代是 A_i = H(A_{i-1}) 单独哈希, 不是 H(A_{i-1}||I);
//   - I 块更新是 (I_j + B + 1) mod 2^v 的**加法**, 不是异或。
func pkcs12KDF(pass, salt []byte, id byte, iter, keyLen int, h func() hash.Hash) []byte {
	const v = 64
	u := h().Size()

	D := bytes.Repeat([]byte{id}, v)
	S := fillWithRepeats(salt, v)
	P := fillWithRepeats(pass, v)
	I := append(append([]byte{}, S...), P...) // 长度 (s+p)*v

	c := (keyLen + u - 1) / u
	var out []byte
	for i := 0; i < c; i++ {
		sum := h()
		sum.Write(D)
		sum.Write(I)
		A := sum.Sum(nil)
		for j := 1; j < iter; j++ {
			s2 := h()
			s2.Write(A)
			A = s2.Sum(nil)
		}
		out = append(out, A...)

		if i == c-1 {
			break
		}
		// B = A 重复到 v 字节
		B := make([]byte, 0, v)
		for len(B) < v {
			B = append(B, A...)
		}
		B = B[:v]
		// I_j = (I_j + B + 1) mod 2^v
		for j := 0; j+v <= len(I); j += v {
			addOneMod(I[j:j+v], B)
		}
	}
	return out[:keyLen]
}

// addOneMod: dst = (dst + B + 1) mod 2^(8*len)，按字节带进位加法。
func addOneMod(dst, b []byte) {
	carry := 1 // 那个 +1
	for i := len(dst) - 1; i >= 0; i-- {
		sum := int(dst[i]) + int(b[i]) + carry // 必须 int 运算 —— byte 会先回绕丢进位
		carry = sum >> 8
		dst[i] = byte(sum)
	}
	// 最高位溢出的进位直接丢弃 = mod 2^v
}

func fillWithRepeats(pattern []byte, v int) []byte {
	if len(pattern) == 0 {
		return nil
	}
	n := ((len(pattern) + v - 1) / v) * v
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = pattern[i%len(pattern)]
	}
	return out
}

// ---- PKCS#12 PBE（pbeWithSHAAnd3-KeyTripleDES-CBC）----

// pbeEncrypt 按算法 OID 派生 3DES 密钥/IV 并加密 data（PKCS#7 填充）。
func pbeEncrypt(algOid asn1.ObjectIdentifier, data, passBytes, salt []byte, iter int) ([]byte, error) {
	key := pkcs12KDF(passBytes, salt, 1, iter, 24, sha1.New)
	iv := pkcs12KDF(passBytes, salt, 2, iter, 8, sha1.New)
	block, err := des.NewTripleDESCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(data, block.BlockSize())
	out := make([]byte, len(padded))
	encryptCBC(block, iv, padded, out)
	return out, nil
}

func encryptCBC(block cipher.Block, iv, src, dst []byte) {
	prev := append([]byte{}, iv...)
	bs := block.BlockSize()
	for i := 0; i < len(src); i += bs {
		chunk := src[i : i+bs]
		for k := 0; k < bs; k++ {
			chunk[k] ^= prev[k]
		}
		block.Encrypt(dst[i:i+bs], chunk)
		prev = dst[i : i+bs]
	}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// ---- 小工具 ----

func sha1SumBytes(b []byte) []byte {
	s := sha1.Sum(b)
	return s[:]
}

// bmpString: PKCS#12 密码用 BMPString（UTF-16 大端 + NULL 终止符）。
// RFC 7292 B.1 明确要求带终止符 —— 漏掉它 KDF 就对不上（踩过）。
func bmpString(s string) []byte {
	out := utf16BE(s)
	return append(out, 0, 0)
}

func utf16BE(s string) []byte {
	runes := utf16.Encode([]rune(s))
	out := make([]byte, len(runes)*2)
	for i, r := range runes {
		binary.BigEndian.PutUint16(out[i*2:], r)
	}
	return out
}

// bmpRawValue: BMPString 的 DER 原始编码（Universal 30）。
// 属性值不带 NULL 终止符（那是密码派生的特殊要求, ASN.1 字符串不需要）。
func bmpRawValue(s string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassUniversal, Tag: 30, Bytes: utf16BE(s)}
}

func octetRawValue(b []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagOctetString, Bytes: b}
}

// octetExplicit: 包一层 [0] EXPLICIT OCTET STRING —— ContentInfo.content 的形状。
func octetExplicit(inner []byte) asn1.RawValue {
	osBytes, _ := asn1.Marshal(inner)
	return asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: osBytes} // Class 2 = context-specific
}

// explicitTag0: [0] EXPLICIT 包装（bagValue 等 EXPLICIT 元素通用）。
func explicitTag0(inner []byte) asn1.RawValue {
	return asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: inner}
}
