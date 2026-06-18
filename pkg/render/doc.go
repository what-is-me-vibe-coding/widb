// Package render 提供 widb 查询响应的多格式渲染能力。
//
// 支持 pretty（ClickHouse 风格圆角表格）、vertical（垂直行块）、
// json、csv 四种格式。cmd/cli 与 cmd/widb 共享本包以避免渲染逻辑重复。
package render
