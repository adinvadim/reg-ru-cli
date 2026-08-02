package cdp

const regAPIIPSyncProgram = `async function(args) {
	const probeBuild = async () => {
		if (location.origin !== "https://www.reg.ru" || !document.body) return {ok: false};
		const renderDeadline = Date.now() + 5000;
		while (!document.querySelector("h1") && Date.now() < renderDeadline) {
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
		const urls = new Set(Array.from(document.scripts).map((script) => script.src).filter(Boolean));
		for (const entry of performance.getEntriesByType("resource")) urls.add(entry.name);
		const candidates = [];
		for (const value of Array.from(urls)) {
			let parsed;
			try { parsed = new URL(value, location.href); } catch (_) { continue; }
			if (parsed.origin !== "https://www.reg.ru" || !parsed.pathname.startsWith("/user/account/") || !parsed.pathname.endsWith(".js")) continue;
			candidates.push(parsed.href);
		}
		const sources = await Promise.all(candidates.slice(0, 64).map(async (href) => {
			try {
				const response = await fetch(href, {credentials: "omit", cache: "force-cache"});
				if (!response.ok) return "";
				const source = await response.text();
				return source.length <= 10 * 1024 * 1024 ? source : "";
			} catch (_) { return ""; }
		}));
		const has = (marker) => sources.some((source) => source.includes(marker));
		const markers = [
			"userSettingApiIPsAdd", "settingsApi", "currentIP", "logout_other_sessions",
			"acc-csrftoken", "x-acc-csrftoken", "/account/issue_csrf_token",
		];
		return {ok: markers.every(has)};
	};
	const buildProbe = await probeBuild();
	if (!buildProbe.ok) return {state: "drift"};

	const readCookie = (name) => {
		const prefix = name + "=";
		for (const part of document.cookie.split(";")) {
			const value = part.trim();
			if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
		}
		return "";
	};
	const parseIPv4 = (value) => {
		if (typeof value !== "string" || !/^(?:0|[1-9][0-9]{0,2})(?:\.(?:0|[1-9][0-9]{0,2})){3}$/.test(value)) return null;
		const octets = value.split(".").map(Number);
		if (octets.some((octet) => octet > 255)) return null;
		return octets.reduce((result, octet) => ((result << 8) | octet) >>> 0, 0);
	};
	const prefixMask = (prefix) => prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
	const dottedMask = (value) => {
		const mask = parseIPv4(value);
		if (mask === null) return null;
		const inverse = (~mask) >>> 0;
		if (((inverse & ((inverse + 1) >>> 0)) >>> 0) !== 0) return null;
		return mask;
	};
	const covers = (entry, address) => {
		if (typeof entry !== "string") return false;
		const normalized = entry.trim();
		const exact = parseIPv4(normalized);
		if (exact !== null) return exact === address;
		if (normalized.endsWith(".")) {
			const octets = normalized.slice(0, -1).split(".");
			if (octets.length >= 1 && octets.length <= 3 && octets.every((octet) => /^(?:0|[1-9][0-9]{0,2})$/.test(octet) && Number(octet) <= 255)) {
				const network = parseIPv4(octets.concat(Array(4 - octets.length).fill("0")).join("."));
				const mask = prefixMask(octets.length * 8);
				return network !== null && ((network & mask) >>> 0) === ((address & mask) >>> 0);
			}
		}
		const parts = normalized.split("/");
		if (parts.length !== 2) return false;
		const network = parseIPv4(parts[0]);
		if (network === null) return false;
		let mask = null;
		if (/^(?:0|[1-9][0-9]?)$/.test(parts[1])) {
			const prefix = Number(parts[1]);
			if (prefix <= 32) mask = prefixMask(prefix);
		} else {
			mask = dottedMask(parts[1]);
		}
		return mask !== null && ((network & mask) >>> 0) === ((address & mask) >>> 0);
	};
	if (!args || typeof args.egressIPv4 !== "string") return {state: "drift"};

	let accCSRF = readCookie("acc-csrftoken");
	if (!accCSRF) {
		let csrfResponse;
		try {
			csrfResponse = await fetch("https://gql-acc.svc.reg.ru/account/issue_csrf_token", {credentials: "include"});
		} catch (_) { return {state: "network"}; }
		if (csrfResponse.status === 401 || csrfResponse.status === 403) return {state: "unauthorized"};
		if (!csrfResponse.ok) return {state: "network"};
		await csrfResponse.text();
		accCSRF = readCookie("acc-csrftoken");
	}
	if (!accCSRF) return {state: "drift"};

	const graphql = async (operationName, query, variables) => {
		let response;
		try {
			response = await fetch("https://gql-acc.svc.reg.ru/?opName=" + encodeURIComponent(operationName), {
				method: "POST",
				credentials: "include",
				headers: {
					"Apollo-Require-Preflight": "true",
					"content-type": "application/json",
					"x-acc-csrftoken": accCSRF,
				},
				body: JSON.stringify({operationName, query, variables: variables || {}}),
			});
		} catch (_) { return {state: "network"}; }
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) return {state: "network"};
		let envelope;
		try { envelope = await response.json(); } catch (_) { return {state: "drift"}; }
		if (!envelope || envelope.errors || !envelope.data) return {state: "rejected"};
		return {state: "ok", data: envelope.data};
	};
	const userQuery = "query user { user { currentIP } }";
	const settingsQuery = "query settingsApi { settingsApi { ipWhitelist } }";
	const userResult = await graphql("user", userQuery, {});
	if (userResult.state !== "ok") return {state: userResult.state};
	const settingsResult = await graphql("settingsApi", settingsQuery, {});
	if (settingsResult.state !== "ok") return {state: settingsResult.state};
	const currentIP = userResult.data && userResult.data.user && userResult.data.user.currentIP;
	const rawTargets = [currentIP, args.egressIPv4];
	const targets = [];
	const seenTargets = new Set();
	for (const raw of rawTargets) {
		const address = parseIPv4(raw);
		if (address === null) return {state: "drift"};
		if (seenTargets.has(address)) continue;
		seenTargets.add(address);
		targets.push({raw, address});
	}
	const whitelist = settingsResult.data && settingsResult.data.settingsApi && settingsResult.data.settingsApi.ipWhitelist;
	if (!Array.isArray(whitelist) || whitelist.some((entry) => typeof entry !== "string")) return {state: "drift"};
	const missingTargets = targets.filter((target) => !whitelist.some((entry) => covers(entry, target.address)));
	if (missingTargets.length === 0) return {state: "unchanged"};

	let loginCSRF = readCookie("csrftoken");
	if (!loginCSRF) {
		try { await fetch("https://login.reg.ru/authenticate", {credentials: "include"}); } catch (_) { return {state: "network"}; }
		loginCSRF = readCookie("csrftoken");
	}
	if (!loginCSRF) return {state: "drift"};
	let logoutOtherSessions;
	try {
		logoutOtherSessions = await fetch("https://login.reg.ru/logout_other_sessions", {
			method: "POST",
			credentials: "include",
			headers: {"x-csrf-token": loginCSRF},
		});
	} catch (_) { return {state: "network"}; }
	if (logoutOtherSessions.status === 401 || logoutOtherSessions.status === 403) return {state: "unauthorized"};
	if (!logoutOtherSessions.ok) return {state: "network"};
	await logoutOtherSessions.text();

	const mutation = [
		"mutation userSettingApiIPsAdd($ips: [String!]!) {",
		"  userSettingApiIPsAdd(ips: $ips) {",
		"    is_success errors { code field text title type }",
		"  }",
		"}",
	].join("\n");
	let mutationResult;
	try {
		mutationResult = await graphql("userSettingApiIPsAdd", mutation, {ips: missingTargets.map((target) => target.raw)});
	} catch (_) {
		return {state: "unknown"};
	}
	if (mutationResult.state === "network" || mutationResult.state === "drift") return {state: "unknown"};
	if (mutationResult.state !== "ok") return {state: mutationResult.state};
	const outcome = mutationResult.data && mutationResult.data.userSettingApiIPsAdd;
	if (!outcome || outcome.is_success !== true || !Array.isArray(outcome.errors) || outcome.errors.length !== 0) return {state: "rejected"};

	const verification = await graphql("settingsApi", settingsQuery, {});
	if (verification.state !== "ok") return {state: "unknown"};
	const verifiedWhitelist = verification.data && verification.data.settingsApi && verification.data.settingsApi.ipWhitelist;
	if (!Array.isArray(verifiedWhitelist) || !targets.every((target) => verifiedWhitelist.some((entry) => covers(entry, target.address)))) return {state: "unknown"};
	return {state: "added"};
}`
