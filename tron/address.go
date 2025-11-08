package tron

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/btcsuite/btcutil/base58"
)

// AddressToParameter 将 TRON Base58 地址转换为 ABI 参数格式（32字节 HEX）
func AddressToParameter(address string) (string, error) {
	// 解码 Base58 地址
	decoded := base58.Decode(address)
	if len(decoded) < 21 {
		return "", errors.New("无效的 TRON 地址")
	}

	// TRON 地址结构：1字节版本(41) + 20字节地址主体 + 4字节校验码
	// 对于 balanceOf(address) 的参数，我们需要20字节的地址主体（跳过版本字节）
	addressBytes := decoded[1:21] // 跳过版本字节，取20字节地址主体

	// 转换为 ABI 编码格式：前12字节填充0，后20字节是地址
	// 总共32字节（64个hex字符）
	param := make([]byte, 32)
	copy(param[12:], addressBytes)

	// 转换为 HEX 字符串
	return hex.EncodeToString(param), nil
}

// ValidateAddress 验证 TRON 地址是否有效
func ValidateAddress(address string) bool {
	decoded := base58.Decode(address)
	if len(decoded) != 25 {
		return false
	}

	// 分离地址和校验码
	addrBytes := decoded[:21]
	checkSum := decoded[21:]

	// 计算校验码（TRON 使用标准 SHA256，不是 SHA3）
	firstHash := sha256.Sum256(addrBytes)
	secondHash := sha256.Sum256(firstHash[:])

	// 检查校验码（取第二次哈希的前4字节）
	for i := 0; i < 4; i++ {
		if checkSum[i] != secondHash[i] {
			return false
		}
	}

	return true
}

// ValidateAddressWithError 验证地址并返回错误信息
func ValidateAddressWithError(address string) error {
	decoded := base58.Decode(address)
	if len(decoded) != 25 {
		return errors.New("地址长度不正确")
	}

	addrBytes := decoded[:21]
	checkSum := decoded[21:]

	// 计算校验码（TRON 使用标准 SHA256，不是 SHA3）
	firstHash := sha256.Sum256(addrBytes)
	secondHash := sha256.Sum256(firstHash[:])

	for i := 0; i < 4; i++ {
		if checkSum[i] != secondHash[i] {
			return errors.New("地址校验码错误")
		}
	}

	return nil
}

// AddressToHex 将 TRON Base58 地址转换为 hex 格式（用于 API 调用）
func AddressToHex(address string) (string, error) {
	decoded := base58.Decode(address)
	if len(decoded) < 21 {
		return "", errors.New("无效的 TRON 地址")
	}

	// TRON 地址在 triggerconstantcontract 中应该使用21字节（包含版本字节41）
	// 解码后的地址结构：1字节版本(41) + 20字节地址主体 + 4字节校验码
	// 对于 owner_address，我们需要21字节（版本+地址主体）
	addressBytes := decoded[:21] // 包含版本字节的21字节

	// 转换为 hex 字符串（不带 0x 前缀，TRON API 要求，应该是42个字符）
	return hex.EncodeToString(addressBytes), nil
}
