package cdp

const billingHistoryProgram = `async function() {
	const probeBuild = async () => {
		if (location.origin !== "https://www.reg.ru" || !document.body) return false;
		if (!location.hash.includes("/balance/bills")) {
			location.hash = "/balance/bills";
			await new Promise((resolve) => setTimeout(resolve, 1500));
		}
		const urls = new Set(Array.from(document.scripts).map((script) => script.src).filter(Boolean));
		for (const entry of performance.getEntriesByType("resource")) urls.add(entry.name);
		const sources = [];
		for (const value of Array.from(urls).slice(0, 128)) {
			let parsed;
			try { parsed = new URL(value, location.href); } catch (_) { continue; }
			if (parsed.origin !== "https://www.reg.ru" || !parsed.pathname.startsWith("/user/account/") || !parsed.pathname.endsWith(".js")) continue;
			try {
				const response = await fetch(parsed.href, {credentials: "omit", cache: "no-store"});
				if (!response.ok) continue;
				const source = await response.text();
				if (source.length <= 10 * 1024 * 1024) sources.push(source);
			} catch (_) {}
		}
		const has = (marker) => sources.some((source) => source.includes(marker));
		const markers = [
			"acc-csrftoken", "x-acc-csrftoken", "/account/issue_csrf_token", "opName", "userBills",
			"has_more", "total_count", "/balance/bills", "/billing/payment/choose", "bill_sid",
		];
		const missing = markers.filter((marker) => !has(marker));
		return {ok: missing.length === 0, missing};
	};
	const buildProbe = await probeBuild();
	if (!buildProbe.ok) return {state: "drift", probe: "build", missing: buildProbe.missing};
	const readCookie = (name) => {
		const prefix = name + "=";
		for (const part of document.cookie.split(";")) {
			const value = part.trim();
			if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
		}
		return "";
	};
	let csrf = readCookie("acc-csrftoken");
	if (!csrf) {
		let csrfResponse;
		try {
			csrfResponse = await fetch("https://gql-acc.svc.reg.ru/account/issue_csrf_token", {credentials: "include"});
		} catch (_) { return {state: "network"}; }
		if (csrfResponse.status === 401 || csrfResponse.status === 403) return {state: "unauthorized"};
		if (!csrfResponse.ok) return {state: "network", status: csrfResponse.status};
		await csrfResponse.text();
		csrf = readCookie("acc-csrftoken");
	}
	if (!csrf) return {state: "drift"};
	const query = [
		"query userBills($limit: Int!, $offset: Int!, $searchQuery: String, $startDate: String, $finishDate: String) {",
		"  userBills(limit: $limit, offset: $offset, searchQuery: $searchQuery, startDate: $startDate, finishDate: $finishDate) {",
		"    has_more total_count",
		"    items { id amount state freezed pay_status pay_type pay_type_title submode is_prepayment isDownloadGarantLetter: is_download_garant_letter }",
		"  }",
		"}",
	].join("\n");
	const items = [];
	const ids = new Set();
	let offset = 0;
	let expectedTotal = null;
	for (let page = 0; page < 21; page++) {
		let response;
		try {
			response = await fetch("https://gql-acc.svc.reg.ru/?opName=userBills", {
				method: "POST",
				credentials: "include",
				headers: {
					"Apollo-Require-Preflight": "true",
					"content-type": "application/json",
					"x-acc-csrftoken": csrf,
				},
				body: JSON.stringify({
					operationName: "userBills",
					query,
					variables: {limit: 50, offset, searchQuery: "", startDate: "", finishDate: ""},
				}),
			});
		} catch (_) {
			return {state: "network"};
		}
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) return {state: "network", status: response.status};
		let envelope;
		try { envelope = await response.json(); } catch (_) { return {state: "drift"}; }
		const bills = envelope && !envelope.errors && envelope.data && envelope.data.userBills;
		if (!bills || typeof bills.has_more !== "boolean" || !Number.isInteger(bills.total_count) || bills.total_count < 0 || !Array.isArray(bills.items)) {
			return {state: "drift"};
		}
		if (expectedTotal === null) expectedTotal = bills.total_count;
		if (expectedTotal !== bills.total_count) return {state: "drift"};
		for (const item of bills.items) {
			if (!item || !Number.isSafeInteger(item.id) || item.id <= 0 || !Number.isFinite(item.amount) ||
				typeof item.state !== "string" ||
				typeof item.freezed !== "boolean" || typeof item.pay_status !== "string" ||
				typeof item.pay_type !== "string" || typeof item.pay_type_title !== "string" ||
				typeof item.submode !== "string" || typeof item.is_prepayment !== "boolean" ||
				typeof item.isDownloadGarantLetter !== "boolean") {
				return {state: "drift"};
			}
			const id = String(item.id);
			if (ids.has(id)) return {state: "drift"};
			ids.add(id);
			items.push({
				id,
				amount: String(item.amount),
				state: item.state,
				freezed: item.freezed,
				payStatus: item.pay_status,
				isPrepayment: item.is_prepayment,
			});
		}
		if (!bills.has_more) {
			if (items.length !== expectedTotal) return {state: "drift"};
			return {state: "available", items};
		}
		if (bills.items.length === 0) return {state: "drift"};
		offset += bills.items.length;
	}
	return {state: "drift"};
}`

const billingCheckoutProgram = `async function(args) {
	if (!args || typeof args.invoiceId !== "string" || !/^[1-9][0-9]*$/.test(args.invoiceId)) {
		return {state: "drift"};
	}
	const probeBuild = async () => {
		if (location.origin !== "https://www.reg.ru" || !document.body) return false;
		if (!location.hash.includes("/balance/bills")) {
			location.hash = "/balance/bills";
			await new Promise((resolve) => setTimeout(resolve, 1500));
		}
		const urls = new Set(Array.from(document.scripts).map((script) => script.src).filter(Boolean));
		for (const entry of performance.getEntriesByType("resource")) urls.add(entry.name);
		const sources = [];
		for (const value of Array.from(urls).slice(0, 128)) {
			let parsed;
			try { parsed = new URL(value, location.href); } catch (_) { continue; }
			if (parsed.origin !== "https://www.reg.ru" || !parsed.pathname.startsWith("/user/account/") || !parsed.pathname.endsWith(".js")) continue;
			try {
				const response = await fetch(parsed.href, {credentials: "omit", cache: "no-store"});
				if (!response.ok) continue;
				const source = await response.text();
				if (source.length <= 10 * 1024 * 1024) sources.push(source);
			} catch (_) {}
		}
		const has = (marker) => sources.some((source) => source.includes(marker));
		const markers = [
			"acc-csrftoken", "x-acc-csrftoken", "/account/issue_csrf_token", "opName", "userBills",
			"has_more", "total_count", "/balance/bills", "/billing/payment/choose", "bill_sid",
		];
		const missing = markers.filter((marker) => !has(marker));
		return {ok: missing.length === 0, missing};
	};
	const buildProbe = await probeBuild();
	if (!buildProbe.ok) return {state: "drift", probe: "build", missing: buildProbe.missing};
	const readCookie = (name) => {
		const prefix = name + "=";
		for (const part of document.cookie.split(";")) {
			const value = part.trim();
			if (value.startsWith(prefix)) return decodeURIComponent(value.slice(prefix.length));
		}
		return "";
	};
	let csrf = readCookie("acc-csrftoken");
	if (!csrf) {
		let csrfResponse;
		try {
			csrfResponse = await fetch("https://gql-acc.svc.reg.ru/account/issue_csrf_token", {credentials: "include"});
		} catch (_) { return {state: "network"}; }
		if (csrfResponse.status === 401 || csrfResponse.status === 403) return {state: "unauthorized"};
		if (!csrfResponse.ok) return {state: "network", status: csrfResponse.status};
		await csrfResponse.text();
		csrf = readCookie("acc-csrftoken");
	}
	if (!csrf) return {state: "drift"};
	const query = [
		"query userBills($limit: Int!, $offset: Int!, $searchQuery: String, $startDate: String, $finishDate: String) {",
		"  userBills(limit: $limit, offset: $offset, searchQuery: $searchQuery, startDate: $startDate, finishDate: $finishDate) {",
		"    has_more total_count items { id state bill_sid freezed pay_status }",
		"  }",
		"}",
	].join("\n");
	let offset = 0;
	let match = null;
	for (let page = 0; page < 21; page++) {
		let response;
		try {
			response = await fetch("https://gql-acc.svc.reg.ru/?opName=userBills", {
				method: "POST",
				credentials: "include",
				headers: {
					"Apollo-Require-Preflight": "true",
					"content-type": "application/json",
					"x-acc-csrftoken": csrf,
				},
				body: JSON.stringify({
					operationName: "userBills",
					query,
					variables: {limit: 50, offset, searchQuery: "", startDate: "", finishDate: ""},
				}),
			});
		} catch (_) {
			return {state: "network"};
		}
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) return {state: "network", status: response.status};
		let envelope;
		try { envelope = await response.json(); } catch (_) { return {state: "drift"}; }
		const bills = envelope && !envelope.errors && envelope.data && envelope.data.userBills;
		if (!bills || typeof bills.has_more !== "boolean" || !Number.isInteger(bills.total_count) || !Array.isArray(bills.items)) {
			return {state: "drift"};
		}
		for (const item of bills.items) {
			if (!item || !Number.isSafeInteger(item.id) || typeof item.state !== "string" ||
				typeof item.bill_sid !== "string" || typeof item.freezed !== "boolean" ||
				typeof item.pay_status !== "string") return {state: "drift"};
			if (String(item.id) === args.invoiceId) {
				if (match) return {state: "drift"};
				match = item;
			}
		}
		if (!bills.has_more) break;
		if (bills.items.length === 0) return {state: "drift"};
		offset += bills.items.length;
	}
	if (!match) return {state: "not-found"};
	if (match.state === "paid" || match.pay_status === "payed") return {state: "already-paid"};
	if (match.freezed || match.pay_status === "onhold") return {state: "not-payable"};
	if (match.state !== "notpaid" || match.pay_status !== "notpayed" || !match.bill_sid) {
		return {state: "checkout-unavailable"};
	}
	const destination = new URL("/billing/payment/choose", "https://www.reg.ru");
	destination.searchParams.set("bill_sid", match.bill_sid);
	setTimeout(() => window.location.assign(destination.href), 0);
	return {state: "browser-opened", shareable: false};
}`
