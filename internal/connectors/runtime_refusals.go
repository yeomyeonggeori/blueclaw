package connectors

import (
	"fmt"
	"strings"
)

// This runtime matches an inbound account against the people this device carries. Whether
// someone was invited is decided elsewhere — the account directory the device is projected
// from — so a refusal here states the match that failed and stops there. Saying "not invited"
// asserts a fact this process cannot read, and it was wrong for the ordinary case: a person
// who is invited, whose messenger account presents an address their record does not carry.
func unmatchedAccountReplyOpeningFor(locale string) string {
	if locale == "ko" {
		return "이 김인턴은 회원님의 계정을 알고 있는 사람과 연결하지 못했습니다. 관리자에게 확인을 요청하세요."
	}
	return "This Intern Kim could not match your account to anyone it knows about. Ask the administrator to check it."
}

// A lookup that never answered established nothing about the sender, so the reply says
// that instead of that nobody knows them. Sending somebody to an administrator over a
// lookup that failed wastes both their time on a record that is already correct.
func directoryUnreachableReplyFor(locale string) string {
	if locale == "ko" {
		return "이 김인턴이 방금 디렉터리에 연결하지 못해 계정을 확인할 수 없습니다. 회원님의 계정 자체에는 문제가 없는 것으로 보입니다. 잠시 후 다시 시도해 보시고, 계속되면 관리자에게 알려주세요."
	}
	return "This Intern Kim could not reach the directory just now, so it cannot tell whose account this is. As far as it knows there is nothing wrong with your account. Try again in a moment, and tell the administrator if it keeps happening."
}

func noEmailAddressReplySuffixFor(locale string) string {
	if locale == "ko" {
		return " 회원님의 계정에는 이메일 주소가 없고, 이 김인턴은 이메일 주소로 계정을 대조합니다."
	}
	return " Your account presents no email address, and that is what this Intern Kim matches on."
}

func unmatchedEmailAddressReplyTemplateFor(locale string) string {
	if locale == "ko" {
		return "%s 회원님의 계정은 %s 주소를 사용 중인데, 이 주소로 등록된 사람이 없습니다 — 다른 주소로 등록되어 있거나, 이 %s 계정이 아직 이 김인턴과 연결되지 않았을 수 있습니다."
	}
	return "%s Your account presents %s, and no one here is on file under that address — either it is recorded under a different one, or this %s account has not reached this Intern Kim yet."
}

// The sender already knows their own address, so naming it discloses nothing to them and
// turns an unanswerable message into one an administrator can act on. Which people this
// device carries stays unsaid, because whoever is asking may be from outside the company.
func unmatchedAccountReplyFor(authorization senderAuthorization, locale string) string {
	locale = normalizeCompanyReplyLocale(locale)
	if authorization.DirectoryUnreachable {
		return directoryUnreachableReplyFor(locale)
	}
	opening := unmatchedAccountReplyOpeningFor(locale)
	platformAccountEmail := strings.TrimSpace(authorization.PlatformAccountEmail)
	if platformAccountEmail == "" {
		return opening + noEmailAddressReplySuffixFor(locale)
	}
	return fmt.Sprintf(unmatchedEmailAddressReplyTemplateFor(locale),
		opening, platformAccountEmail, strings.TrimSpace(authorization.Platform))
}

func normalizeCompanyReplyLocale(locale string) string {
	if strings.EqualFold(strings.TrimSpace(locale), "ko") {
		return "ko"
	}
	return "en"
}

// senderAuthorization is what the runtime actually established about an inbound sender, so a
// refusal can state that and nothing further.
type senderAuthorization struct {
	PersonID             string
	IsAllowed            bool
	Platform             string
	PlatformAccountEmail string
	// DirectoryUnreachable separates a directory that said no from one that never
	// answered. Told they are not on file, somebody goes to an administrator who
	// finds their record exactly where it belongs, and nothing anywhere says the
	// lookup is what failed.
	DirectoryUnreachable bool
}
