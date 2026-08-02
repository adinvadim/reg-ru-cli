package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

const (
	programAuthProbe       session.ProgramID = "portal.auth.probe"
	programLogout          session.ProgramID = "portal.auth.logout"
	programREGAPIIPSync    session.ProgramID = "portal.auth.regapi-ip-sync"
	programS3Inventory     session.ProgramID = "portal.s3.inventory"
	programS3Mutation      session.ProgramID = "portal.s3.mutation"
	programS3Credentials   session.ProgramID = "portal.s3.credentials"
	programBillingHistory  session.ProgramID = "portal.billing.history"
	programBillingCheckout session.ProgramID = "portal.billing.checkout"
	programSupportRead     session.ProgramID = "portal.support.read"
	programSupportMutation session.ProgramID = "portal.support.mutation"
)

var firstPartyOrigins = []string{
	"https://www.reg.ru",
	"https://cloud.reg.ru",
	"https://login.reg.ru",
}

func productionPrograms() map[session.ProgramID]program {
	return map[session.ProgramID]program{
		programAuthProbe: {
			source:         authProbeProgram,
			maxResultBytes: 1024,
			allowedOrigins: firstPartyOrigins,
		},
		programLogout: {
			source:         logoutProgram,
			maxResultBytes: 1024,
			allowedOrigins: firstPartyOrigins,
		},
		programREGAPIIPSync: {
			source:         regAPIIPSyncProgram,
			maxResultBytes: 1024,
			allowedOrigins: firstPartyOrigins,
		},
		programS3Inventory: {
			source:         s3InventoryProgram,
			maxResultBytes: 64 << 10,
			allowedOrigins: firstPartyOrigins,
		},
		programS3Mutation: {
			source:         s3MutationProgram,
			maxResultBytes: 64 << 10,
			allowedOrigins: firstPartyOrigins,
		},
		programS3Credentials: {
			source:         s3CredentialsProgram,
			maxResultBytes: 8 << 10,
			allowedOrigins: firstPartyOrigins,
		},
		programBillingHistory: {
			source:         billingHistoryProgram,
			maxResultBytes: 64 << 10,
			allowedOrigins: firstPartyOrigins,
		},
		programBillingCheckout: {
			source:         billingCheckoutProgram,
			maxResultBytes: 1024,
			allowedOrigins: firstPartyOrigins,
		},
		programSupportRead: {
			source:         supportReadProgram,
			maxResultBytes: 256 << 10,
			allowedOrigins: firstPartyOrigins,
		},
		programSupportMutation: {
			source:         supportMutationProgram,
			maxResultBytes: 8 << 10,
			allowedOrigins: firstPartyOrigins,
		},
	}
}

func (b *browser) WaitForAuthentication(
	ctx context.Context,
	key []byte,
) (session.Observation, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		observation, err := b.probe(ctx, key, programAuthProbe)
		if err != nil {
			if ctx.Err() != nil {
				return session.Observation{}, ctx.Err()
			}
			if !errors.Is(err, errPageTransition) {
				return session.Observation{}, err
			}
		}
		if err == nil && observation.State != session.ObservedNoSession {
			return observation, nil
		}
		select {
		case <-ctx.Done():
			return session.Observation{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *browser) Refresh(
	ctx context.Context,
	key []byte,
) (session.Observation, error) {
	return b.probe(ctx, key, programAuthProbe)
}

func (b *browser) Logout(
	ctx context.Context,
	key []byte,
) (session.Observation, error) {
	return b.probe(ctx, key, programLogout)
}

func (b *browser) probe(
	ctx context.Context,
	key []byte,
	id session.ProgramID,
) (session.Observation, error) {
	if len(key) == 0 {
		return session.Observation{}, errors.New("portal identity key is unavailable")
	}
	args, err := json.Marshal(map[string]string{
		"key": base64.StdEncoding.EncodeToString(key),
	})
	if err != nil {
		return session.Observation{}, errors.New("encode portal probe arguments")
	}
	var result json.RawMessage
	if err := b.Executor().RunJSON(ctx, id, args, &result); err != nil {
		return session.Observation{}, err
	}
	var reduced struct {
		State  string `json:"state"`
		Digest string `json:"digest,omitempty"`
		Login  string `json:"login,omitempty"`
	}
	if err := json.Unmarshal(result, &reduced); err != nil {
		return session.Observation{
			State: session.ObservedIncompatible,
		}, nil
	}
	switch reduced.State {
	case "authenticated":
		if reduced.Login == "" {
			return session.Observation{State: session.ObservedIncompatible}, nil
		}
		digest, err := base64.RawStdEncoding.DecodeString(reduced.Digest)
		if err != nil {
			return session.Observation{State: session.ObservedIncompatible}, nil
		}
		return session.Observation{
			State:          session.ObservedAuthenticated,
			IdentityDigest: digest,
			ProviderLogin:  reduced.Login,
		}, nil
	case "no-session":
		return session.Observation{State: session.ObservedNoSession}, nil
	default:
		return session.Observation{State: session.ObservedIncompatible}, nil
	}
}

const authProbeProgram = `async function(args) {
	const readCookie = (name) => {
		const prefix = name + "=";
		for (const part of document.cookie.split(";")) {
			const value = part.trim();
			if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
		}
		return "";
	};
	let csrf = readCookie("csrftoken");
	if (!csrf) {
		await fetch("https://login.reg.ru/authenticate", {credentials: "include"});
		csrf = readCookie("csrftoken");
	}
	if (!csrf) return {state: "incompatible"};
	const response = await fetch("https://login.reg.ru/refresh", {
		method: "POST",
		credentials: "include",
		headers: {"x-csrf-token": csrf}
	});
	if (response.status === 401 || response.status === 403) return {state: "no-session"};
	if (!response.ok) throw new Error("portal refresh unavailable");
	const text = await response.text();
	if (text.length > 65536) return {state: "incompatible"};
	let envelope;
	try { envelope = JSON.parse(text); } catch (_) { return {state: "incompatible"}; }
	const value = envelope && envelope.success === true && envelope.result;
	if (!value || typeof value.status !== "string") return {state: "incompatible"};
	if (value.status === "no_user_session") return {state: "no-session"};
	if (value.status !== "session_refreshed") return {state: "incompatible"};
	if (!value.user_id || typeof value.screen_name !== "string" || !value.screen_name) return {state: "incompatible"};
	const identity = JSON.stringify([String(value.user_id), String(value.screen_name)]);
	const keyBytes = Uint8Array.from(atob(args.key.replace(/-/g, "+").replace(/_/g, "/")), c => c.charCodeAt(0));
	const key = await crypto.subtle.importKey("raw", keyBytes, {name: "HMAC", hash: "SHA-256"}, false, ["sign"]);
	const digest = new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(identity)));
	let binary = "";
	for (const byte of digest) binary += String.fromCharCode(byte);
	return {state: "authenticated", digest: btoa(binary).replace(/=+$/, ""), login: value.screen_name};
}`

const logoutProgram = `async function(args) {
	const readCookie = (name) => {
		const prefix = name + "=";
		for (const part of document.cookie.split(";")) {
			const value = part.trim();
			if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
		}
		return "";
	};
	let csrf = readCookie("csrftoken");
	if (!csrf) {
		await fetch("https://login.reg.ru/authenticate", {credentials: "include"});
		csrf = readCookie("csrftoken");
	}
	if (!csrf) return {state: "incompatible"};
	const logout = await fetch("https://login.reg.ru/logout", {
		method: "POST",
		credentials: "include",
		headers: {"x-csrf-token": csrf}
	});
	if (!logout.ok) throw new Error("portal logout unavailable");
	const refresh = await fetch("https://login.reg.ru/refresh", {
		method: "POST",
		credentials: "include",
		headers: {"x-csrf-token": csrf}
	});
	if (refresh.status === 401 || refresh.status === 403) return {state: "no-session"};
	if (!refresh.ok) throw new Error("portal logout verification unavailable");
	const text = await refresh.text();
	if (text.length > 65536) return {state: "incompatible"};
	let envelope;
	try { envelope = JSON.parse(text); } catch (_) { return {state: "incompatible"}; }
	const value = envelope && envelope.success === true && envelope.result;
	if (value && value.status === "no_user_session") return {state: "no-session"};
	return {state: "incompatible"};
}`
