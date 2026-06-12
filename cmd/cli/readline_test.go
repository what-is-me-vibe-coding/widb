package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// TestReadMultiLineSQL_SingleLineWithSemicolon 测试单行以分号结尾的 SQL。
func TestReadMultiLineSQL_SingleLineWithSemicolon(t *testing.T) {
	c := &cli{}
	input := "SELECT * FROM users;\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan() // 读取第一行
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	expected := testSQL
	if result != expected {
		t.Errorf("结果 = %q, 期望 %q", result, expected)
	}
}

// TestReadMultiLineSQL_MultiLine 测试多行 SQL 输入。
func TestReadMultiLineSQL_MultiLine(t *testing.T) {
	c := &cli{}
	input := "SELECT *\nFROM users\nWHERE id = 1;\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan() // 读取第一行 "SELECT *"
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	expected := "SELECT * FROM users WHERE id = 1"
	if result != expected {
		t.Errorf("结果 = %q, 期望 %q", result, expected)
	}
}

// TestReadMultiLineSQL_NoSemicolon 测试没有分号的多行输入（EOF 结束）。
func TestReadMultiLineSQL_NoSemicolon(t *testing.T) {
	c := &cli{}
	input := "SELECT * FROM users\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan() // 读取第一行 "SELECT * FROM users"（无分号）
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	expected := testSQL
	if result != expected {
		t.Errorf("结果 = %q, 期望 %q", result, expected)
	}
}

// TestReadMultiLineSQL_EmptyInput 测试空输入。
func TestReadMultiLineSQL_EmptyInput(t *testing.T) {
	c := &cli{}
	input := ";\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan()
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	// 只有分号，去除后为空
	if result != "" {
		t.Errorf("结果 = %q, 期望空字符串", result)
	}
}

// TestReadMultiLineSQL_MultiLineWithContinuationPrompt 测试多行输入时输出续行提示符。
func TestReadMultiLineSQL_MultiLineWithContinuationPrompt(t *testing.T) {
	c := &cli{}
	input := "SELECT *\nFROM users;\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan()
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	// 验证续行提示符被输出
	if !strings.Contains(out.String(), "...>") {
		t.Errorf("期望输出包含续行提示符 '...>'，实际: %q", out.String())
	}

	expected := testSQL
	if result != expected {
		t.Errorf("结果 = %q, 期望 %q", result, expected)
	}
}

// TestReadMultiLineSQL_WhitespaceHandling 测试空白字符处理。
func TestReadMultiLineSQL_WhitespaceHandling(t *testing.T) {
	c := &cli{}
	input := "  SELECT * FROM users;  \n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	var out bytes.Buffer

	scanner.Scan()
	result := c.readMultiLineSQL(scanner, &out, scanner.Text())

	expected := testSQL
	if result != expected {
		t.Errorf("结果 = %q, 期望 %q", result, expected)
	}
}
