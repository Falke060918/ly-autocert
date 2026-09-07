package upstream

import (
	"context"
	"errors"
	"net/mail"
	"net/url"
	"strings"
)

type Contact struct {
	ExistingID int         `json:"existing_contact_id,omitempty"`
	New        *NewContact `json:"new_contact,omitempty"`
}
type NewContact struct {
	FirstName string   `json:"first_name,omitempty"`
	LastName  string   `json:"last_name"`
	Email     string   `json:"email"`
	Types     []string `json:"contact_types"`
}
type Subscriber struct {
	ExistingID int            `json:"existing_subscriber_id"`
	New        *NewSubscriber `json:"new_subscriber,omitempty"`
	Contact    Contact        `json:"contact"`
}
type NewSubscriber struct {
	Name     string `json:"legal_organization_name"`
	Type     string `json:"subscriber_type"`
	Country  string `json:"country_iso_code2"`
	Locality string `json:"locality"`
	Province string `json:"province"`
	Phone    string `json:"phone_number"`
	Address  string `json:"address"`
}

func (s Subscriber) Validate() error {
	if s.ExistingID < 0 || (s.ExistingID == 0 && s.New == nil) || (s.ExistingID > 0 && s.New != nil) {
		return errors.New("기존 가입자 ID 또는 신규 가입자 정보를 입력하세요")
	}
	if n := s.New; n != nil {
		if n.Name == "" || n.Address == "" || n.Locality == "" || n.Province == "" || n.Phone == "" || len(n.Country) != 2 {
			return errors.New("가입자 회사·국가·주소·지역·전화번호가 필요합니다")
		}
		switch n.Type {
		case "private_organization", "government_entity", "business_entity", "non_commercial_entity":
		default:
			return errors.New("가입자 유형이 올바르지 않습니다")
		}
	}
	if s.Contact.ExistingID > 0 && s.Contact.New == nil {
		return nil
	}
	n := s.Contact.New
	if n == nil || s.Contact.ExistingID != 0 || n.LastName == "" || len(n.Types) == 0 {
		return errors.New("가입자 담당자 정보를 입력하세요")
	}
	a, e := mail.ParseAddress(n.Email)
	if e != nil || a.Address != n.Email {
		return errors.New("담당자 이메일이 올바르지 않습니다")
	}
	organization := false
	for _, t := range n.Types {
		if t != "organization" && t != "technical" && t != "billing" {
			return errors.New("담당자 역할이 올바르지 않습니다")
		}
		organization = organization || t == "organization"
	}
	if !organization {
		return errors.New("주문 신청자에게 organization 역할이 필요합니다")
	}
	return nil
}
func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("CA 주소는 사용자 정보·쿼리가 없는 HTTPS URL이어야 합니다")
	}
	return nil
}

type Validation struct {
	Name   string `json:"name"`
	Method string `json:"method"`
}
type Spec struct {
	DNSNames     []string     `json:"dns_names"`
	Algorithm    string       `json:"key_algorithm"`
	ValidityDays int          `json:"validity_days"`
	ProductID    int          `json:"product_id"`
	SigningCA    string       `json:"signing_ca_type"`
	Validations  []Validation `json:"domain_validations"`
}

func (s Spec) Validate() error {
	if len(s.DNSNames) < 1 || len(s.DNSNames) > 100 || s.ProductID < 1 || s.ValidityDays < 1 || s.ValidityDays > 3650 {
		return errors.New("도메인·상품·유효기간을 확인하세요")
	}
	switch s.Algorithm {
	case "rsa2048", "rsa3072", "rsa4096", "ecdsa_p256", "ecdsa_p384":
	default:
		return errors.New("지원하지 않는 키 알고리즘입니다")
	}
	switch s.SigningCA {
	case "automatic", "rsa", "ecc":
	default:
		return errors.New("서명 CA 유형을 확인하세요")
	}
	names := map[string]bool{}
	for _, n := range s.DNSNames {
		if !DNSName(n) || names[n] {
			return errors.New("도메인 형식 또는 중복을 확인하세요")
		}
		names[n] = true
	}
	if len(s.Validations) != len(names) {
		return errors.New("도메인별 검증 방식이 필요합니다")
	}
	for _, v := range s.Validations {
		if !names[v.Name] || (v.Method != "txt_dns_record" && v.Method != "txt_http_file") || (strings.HasPrefix(v.Name, "*.") && v.Method != "txt_dns_record") {
			return errors.New("도메인 검증 방식을 확인하세요")
		}
		delete(names, v.Name)
	}
	return nil
}
func DNSName(n string) bool {
	if n != strings.ToLower(n) || len(n) > 253 {
		return false
	}
	n = strings.TrimPrefix(n, "*.")
	if !strings.Contains(n, ".") {
		return false
	}
	for _, p := range strings.Split(n, ".") {
		if len(p) == 0 || len(p) > 63 || p[0] == '-' || p[len(p)-1] == '-' {
			return false
		}
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

type Product struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	MaxValidity   *int   `json:"max_order_validity"`
	AllowWildcard bool   `json:"allow_wildcard"`
}
type DomainValidation struct {
	Name   string `json:"domain_name"`
	Method string `json:"validation_method"`
	Key    string `json:"validation_key"`
	TXT    string `json:"dns_txt_record"`
	URL    string `json:"http_file_url"`
	Status string `json:"status"`
}
type Snapshot struct {
	Code        string `json:"order_code"`
	Status      string `json:"order_status"`
	Certificate struct {
		Base64 string `json:"x509_base64"`
		Chain  string `json:"ca_chain"`
	} `json:"order_certificate_info"`
	Validation []DomainValidation `json:"domain_validation_info"`
}
type Connector interface {
	TestConnection(context.Context) error
	ListProducts(context.Context) ([]Product, error)
	PlaceOrder(context.Context, Spec, string, Subscriber) (Snapshot, error)
	FetchOrder(context.Context, string) (Snapshot, error)
	Cancel(context.Context, string) error
}
