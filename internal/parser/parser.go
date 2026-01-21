package parser

import (
	"strings"
)

// Token представляет минимальную единицу разбора
type Token string

// CommandStep представляет шаг пайплайна с командой, аргументами и оператором
type CommandStep struct {
	Command  string
	Args     []string
	Operator string
}

// Pipeline представляет полный пайплайн с шагами и исходной строкой
type Pipeline struct {
	Steps    []CommandStep
	Original string
}

// String собирает пайплайн обратно в строку
func (p *Pipeline) String() string {
	if len(p.Steps) == 0 {
		return ""
	}
	var parts []string
	for i, step := range p.Steps {
		cmd := step.Command
		if len(step.Args) > 0 {
			cmd += " " + strings.Join(step.Args, " ")
		}
		parts = append(parts, cmd)
		if step.Operator != "" && i < len(p.Steps)-1 {
			parts = append(parts, step.Operator)
		}
	}
	return strings.Join(parts, " ")
}

// tokenize разбивает входную строку на токены с учётом кавычек
func tokenize(input string) []string {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	quoteChar := byte(0)
	i := 0
	for i < len(input) {
		ch := input[i]
		switch {
		case !inQuotes && (ch == '"' || ch == '\''):
			inQuotes = true
			quoteChar = ch
		case inQuotes && ch == quoteChar:
			inQuotes = false
			quoteChar = 0
			// Не добавляем кавычку
		case !inQuotes && (ch == ' ' || ch == '\t'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		case !inQuotes && (ch == '|' || ch == '&' || ch == ';' || ch == '>'):
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			// Проверяем на && или ||
			if ch == '&' && i+1 < len(input) && input[i+1] == '&' {
				tokens = append(tokens, "&&")
				i++
			} else if ch == '|' && i+1 < len(input) && input[i+1] == '|' {
				tokens = append(tokens, "||")
				i++
			} else {
				tokens = append(tokens, string(ch))
			}
		default:
			current.WriteByte(ch)
		}
		i++
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// parseSteps парсит токены в CommandStep
func parseSteps(tokens []string) []CommandStep {
	var steps []CommandStep
	i := 0
	for i < len(tokens) {
		if isOperator(tokens[i]) {
			// Оператор без команды перед ним? Пропустить или ошибка
			i++
			continue
		}
		step := CommandStep{}
		// Первый токен - команда
		step.Command = tokens[i]
		i++
		// Собираем args до оператора
		for i < len(tokens) && !isOperator(tokens[i]) {
			step.Args = append(step.Args, tokens[i])
			i++
		}
		// Если есть оператор, устанавливаем его
		if i < len(tokens) && isOperator(tokens[i]) {
			step.Operator = tokens[i]
			i++
		}
		steps = append(steps, step)
	}
	return steps
}

// isOperator проверяет, является ли токен оператором
func isOperator(token string) bool {
	return token == "|" || token == "&&" || token == "||" || token == ";" || token == ">"
}

// Parse разбирает входную строку на Pipeline
func Parse(input string) (*Pipeline, error) {
	tokens := tokenize(input)
	steps := parseSteps(tokens)
	return &Pipeline{Steps: steps, Original: input}, nil
}
