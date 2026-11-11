// Package ipmask reduz a precisão de endereços IP exibidos pela API.
package ipmask

import (
	"fmt"
	"net/netip"
)

// Mode define como IPs são apresentados.
type Mode string

const (
	// None exibe o IP completo (apenas para dados fictícios/demonstração).
	None Mode = "none"
	// Partial exibe apenas o prefixo: /24 para IPv4 e /48 para IPv6.
	Partial Mode = "partial"
)

// ParseMode valida o modo configurado.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case None, Partial:
		return Mode(s), nil
	}
	return "", fmt.Errorf("modo de mascaramento inválido %q (use none|partial)", s)
}

// Apply mascara o IP conforme o modo. Valores que não são IP (ex.: a chave
// de agregação "(other)") são retornados sem alteração.
func (m Mode) Apply(ip string) string {
	if m != Partial {
		return ip
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	bits := 24
	if addr.Is6() {
		bits = 48
	}
	p, err := addr.Prefix(bits)
	if err != nil {
		return ip
	}
	return p.String()
}
