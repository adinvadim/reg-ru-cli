package cloudvps

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"
)

const (
	defaultPollInitial = time.Second
	defaultPollMaximum = 5 * time.Second
)

type WaitOptions struct {
	InitialInterval time.Duration
	MaximumInterval time.Duration
}

func (c *Client) WaitAction(
	ctx context.Context,
	id string,
	options WaitOptions,
) (Action, error) {
	if id == "" {
		return Action{}, fmt.Errorf("CloudVPS action identifier is empty")
	}
	interval := options.InitialInterval
	if interval <= 0 {
		interval = defaultPollInitial
	}
	maximum := options.MaximumInterval
	if maximum <= 0 {
		maximum = defaultPollMaximum
	}
	if maximum < interval {
		maximum = interval
	}

	last := Action{ID: ID(id)}
	for {
		action, err := c.GetAction(ctx, id)
		if err != nil {
			if !isRetryableWaitError(err) {
				return Action{}, err
			}
			if err := c.sleep(ctx, c.jitter(interval)); err != nil {
				return last, &WaitError{Action: last, Cause: err}
			}
			interval = nextPollInterval(interval, maximum)
			continue
		}
		last = action
		terminal, succeeded := action.Terminal()
		if terminal {
			if !succeeded {
				return action, &ActionFailedError{Action: action}
			}
			return action, nil
		}
		if !action.Pending() {
			return action, &ContractError{
				Message: "CloudVPS action returned an unknown status",
			}
		}
		if err := c.sleep(ctx, c.jitter(interval)); err != nil {
			return action, &WaitError{Action: action, Cause: err}
		}
		interval = nextPollInterval(interval, maximum)
	}
}

func nextPollInterval(current, maximum time.Duration) time.Duration {
	current *= 2
	if current > maximum {
		return maximum
	}
	return current
}

func isRetryableWaitError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func defaultJitter(delay time.Duration) time.Duration {
	if delay <= 1 {
		return delay
	}
	// Equal jitter keeps a useful floor while avoiding synchronized polling.
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
