package common

import "errors"

// 预定义错误，供全项目统一使用。
var (
	ErrKeyNotFound    = errors.New("key not found")
	ErrTableNotExist  = errors.New("table does not exist")
	ErrColumnNotExist = errors.New("column does not exist")
	ErrTypeMismatch   = errors.New("type mismatch")
	ErrCorruptedData  = errors.New("corrupted data")
	ErrInvalidSchema  = errors.New("invalid schema")
	ErrDuplicateKey   = errors.New("duplicate key")
	ErrReadOnly       = errors.New("read only")
)
