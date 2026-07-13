package auth

import (
	"net/http"
	"strings"
	"time"
)

type RoutingErrorOwner string
type RoutingErrorAction string

const (
	RoutingOwnerRequest  RoutingErrorOwner = "request"
	RoutingOwnerAccount  RoutingErrorOwner = "account"
	RoutingOwnerModel    RoutingErrorOwner = "model"
	RoutingOwnerProxy    RoutingErrorOwner = "proxy"
	RoutingOwnerUpstream RoutingErrorOwner = "upstream"
	RoutingOwnerLocal    RoutingErrorOwner = "local"

	RoutingActionReturn         RoutingErrorAction = "return"
	RoutingActionRetrySame      RoutingErrorAction = "retry_same"
	RoutingActionSwitch         RoutingErrorAction = "switch"
	RoutingActionCooldown       RoutingErrorAction = "cooldown"
	RoutingActionManualRecovery RoutingErrorAction = "manual_recovery"
)

// RoutingError is the normalized account-pool decision view of an execution error.
type RoutingError struct {
	Err          error
	Owner        RoutingErrorOwner
	Action       RoutingErrorAction
	Status       int
	RetryDelay   *time.Duration
	Reason       string
	UpstreamHead http.Header
}

func (e *RoutingError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *RoutingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *RoutingError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.Status
}

func (e *RoutingError) RetryAfter() *time.Duration {
	if e == nil || e.RetryDelay == nil {
		return nil
	}
	value := *e.RetryDelay
	return &value
}

func (e *RoutingError) Headers() http.Header {
	if e == nil {
		return nil
	}
	return e.UpstreamHead.Clone()
}

func classifyClaudeAccountPoolRoutingError(err error) *RoutingError {
	if err == nil {
		return nil
	}
	routingErr := &RoutingError{
		Err:        err,
		Owner:      RoutingOwnerUpstream,
		Action:     RoutingActionSwitch,
		Status:     statusCodeFromError(err),
		RetryDelay: retryAfterFromError(err),
		Reason:     "upstream_error",
	}
	if headerProvider, ok := err.(interface{ Headers() http.Header }); ok && headerProvider != nil {
		routingErr.UpstreamHead = headerProvider.Headers().Clone()
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if isRequestInvalidError(err) {
		routingErr.Owner = RoutingOwnerRequest
		routingErr.Action = RoutingActionReturn
		routingErr.Reason = "invalid_request"
		return routingErr
	}
	switch routingErr.Status {
	case http.StatusUnauthorized:
		routingErr.Owner = RoutingOwnerAccount
		routingErr.Action = RoutingActionRetrySame
		routingErr.Reason = "unauthorized"
	case http.StatusPaymentRequired:
		routingErr.Owner = RoutingOwnerAccount
		routingErr.Action = RoutingActionManualRecovery
		routingErr.Reason = "billing_required"
	case http.StatusForbidden:
		routingErr.Owner = RoutingOwnerAccount
		routingErr.Action = RoutingActionCooldown
		routingErr.Reason = "forbidden"
		if isCloudflareChallengeErrorMessage(message) {
			routingErr.Owner = RoutingOwnerProxy
			routingErr.Reason = "cloudflare_challenge"
		} else if accountPoolManualRecoveryMessage(message) {
			routingErr.Action = RoutingActionManualRecovery
			routingErr.Reason = accountPoolManualRecoveryReason(message)
		}
	case http.StatusTooManyRequests:
		routingErr.Owner = RoutingOwnerModel
		routingErr.Action = RoutingActionCooldown
		routingErr.Reason = "rate_limited"
	case 529:
		routingErr.Owner = RoutingOwnerModel
		routingErr.Action = RoutingActionRetrySame
		routingErr.Reason = "overloaded"
	default:
		if routingErr.Status >= http.StatusInternalServerError {
			routingErr.Owner = RoutingOwnerUpstream
			routingErr.Action = RoutingActionSwitch
			routingErr.Reason = "upstream_5xx"
		} else if routingErr.Status == 0 && accountPoolTransportFailureMessage(message) {
			routingErr.Owner = RoutingOwnerProxy
			routingErr.Action = RoutingActionSwitch
			routingErr.Reason = "transport_error"
		}
	}
	return routingErr
}
