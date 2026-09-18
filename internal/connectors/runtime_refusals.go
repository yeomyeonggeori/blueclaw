package connectors

import (
	"fmt"
	"strings"
)

func unmatchedAccountReplyOpeningFor(locale string) string {
	if locale == "ko" {
		return "이 김인턴은 회원님의 계정을 알고 있는 사람과 연결하지 못했습니다. 관리자에게 확인을 요청하세요."
	}
	return "This Intern Kim could not match your account to anyone it knows about. Ask the administrator to check it."
}

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

type senderAuthorization struct {
	PersonID             string
	IsAllowed            bool
	Platform             string
	PlatformAccountEmail string
	DirectoryUnreachable bool
}
