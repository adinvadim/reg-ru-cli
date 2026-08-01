package cdp

const supportReadProgram = `async function(args) {
	const text = node => String(node && node.textContent || "").trim();
	const scripts = Array.from(document.scripts).map(script => script.src);
	if (args.action !== "detail" && args.action !== "reconcile" && !scripts.some(src => /\/dist\/support-tickets\.[a-f0-9]+\.js$/.test(src))) return {state: "build-drift"};
	if (location.origin !== "https://www.reg.ru" || !location.pathname.startsWith("/support/tickets")) return {state: "route-drift"};
	if (args.action === "list" || args.action === "reconcile-create") {
		const root = document.querySelector(".b-support-ticket-list");
		if (!root) return {state: args.action === "list" ? "principal-drift" : "operation-drift"};
		if (args.action === "reconcile-create") {
			const exact = Array.from(root.querySelectorAll(".b-support-ticket-new__msg"))
				.filter(node => text(node) === args.message).length;
			return {state: exact === 1 ? "committed" : "ambiguous"};
		}
		const tickets = [];
		for (const row of root.querySelectorAll(".b-support-ticket-new")) {
			const number = row.querySelector(".b-support-ticket-new__number[href]");
			const status = row.querySelector(".b-support-ticket-new__state");
			const preview = row.querySelector(".b-support-ticket-new__msg");
			if (!number || !status || !preview) return {state: "operation-drift"};
			const id = text(number).replace(/^#/, "");
			const href = new URL(number.href, location.href);
			if (!/^[1-9][0-9]*$/.test(id) || href.origin !== location.origin || !/^\/support\/tickets\/[^/]+\/?$/.test(href.pathname)) {
				return {state: "response-drift"};
			}
			tickets.push({id, status: status.classList.contains("b-support-ticket-new__state_status_closed") ? "closed" : "open", preview: text(preview)});
		}
		let filtered = tickets;
		if (args.status && args.status !== "all") filtered = tickets.filter(ticket => ticket.status === args.status);
		const start = Math.max(0, (args.page - 1) * args.limit);
		return {state: "available", total: filtered.length, tickets: filtered.slice(start, start + args.limit)};
	}
	if (args.action === "navigate") {
		const matches = Array.from(document.querySelectorAll(".b-support-ticket-new")).filter(row => {
			const number = row.querySelector(".b-support-ticket-new__number");
			return text(number).replace(/^#/, "") === args.id;
		});
		if (matches.length === 0) return {state: "not-found"};
		if (matches.length !== 1) return {state: "response-drift"};
		const link = matches[0].querySelector(".b-support-ticket-new__number[href]");
		const target = new URL(link.href, location.href);
		if (target.origin !== location.origin || !/^\/support\/tickets\/[^/]+\/?$/.test(target.pathname)) return {state: "route-drift"};
		location.assign(target.href);
		return {state: "navigating"};
	}
	if (args.action !== "detail" && args.action !== "reconcile") return {state: "operation-drift"};
	const root = document.querySelector(".b-support-ticket");
	const title = root && root.querySelector(".b-support-ticket__title");
	const status = root && root.querySelector(".b-support-ticket__state");
	if (!root || !title || !status) return {state: "operation-drift"};
	const messages = [];
	let customerClosed = false;
	const messageNodes = Array.from(root.querySelectorAll(".b-support-ticket__message"));
	if (messageNodes.length === 0) return {state: "operation-drift"};
	for (const node of messageNodes) {
		if (node.classList.contains("b-support-ticket__message-customer-closed")) {
			customerClosed = true;
			continue;
		}
		const body = node.querySelector(".b-support-ticket__message-text");
		if (!body) return {state: "operation-drift"};
		messages.push({
			body: text(body),
			created: text(node.querySelector(".b-support-ticket__message-created")),
			sender: text(node.querySelector(".b-support-ticket__message-sender")),
			kind: node.classList.contains("b-support-ticket__message_style_agent") ? "agent" : "customer"
		});
	}
	if (args.action === "reconcile") {
		if (args.mutation === "reply") {
			const exact = messages.filter(message => message.body === args.message).length;
			return {state: exact === 1 ? "committed" : "ambiguous"};
		}
		if (args.mutation === "close") {
			return {state: status.classList.contains("b-support-ticket__state_color_red") && customerClosed ? "committed" : "ambiguous"};
		}
		return {state: "operation-drift"};
	}
	return {state: "available", ticket: {id: args.id, title: text(title), status: status.classList.contains("b-support-ticket__state_color_red") ? "closed" : "open", messages}};
}`

const supportMutationProgram = `async function(args) {
	let dispatched = false;
	const waitFor = async (read, timeout = 10000) => {
		const end = Date.now() + timeout;
		while (Date.now() < end) {
			const value = read();
			if (value) return value;
			await new Promise(resolve => setTimeout(resolve, 100));
		}
		return null;
	};
	const setText = (field, value) => {
		const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value").set;
		setter.call(field, value);
		field.dispatchEvent(new Event("input", {bubbles: true}));
		field.dispatchEvent(new Event("change", {bubbles: true}));
	};
	try {
		if (location.origin !== "https://www.reg.ru" || !location.pathname.startsWith("/support/tickets")) return {state: "route-drift"};
		if (args.action === "create") {
			const open = document.querySelector("#new-vue-support-ticket-button");
			if (!open) return {state: "operation-drift"};
			open.click();
			const noService = await waitFor(() => document.querySelector(".request-services__link_without-service"));
			if (!noService) return {state: "operation-drift"};
			noService.click();
			const field = await waitFor(() => document.querySelector('textarea[name="message"]'));
			const submit = document.querySelector(".request-form__submit-button");
			if (!field || !submit) return {state: "operation-drift"};
			setText(field, args.message);
			dispatched = true;
			submit.click();
			await new Promise(resolve => setTimeout(resolve, 5000));
			const response = await fetch("/support/tickets/", {credentials: "include"});
			if (!response.ok) return {state: "ambiguous"};
			const page = new DOMParser().parseFromString(await response.text(), "text/html");
			const exact = Array.from(page.querySelectorAll(".b-support-ticket-new__msg")).filter(node => String(node.textContent || "").trim() === args.message).length;
			return exact === 1 ? {state: "committed"} : {state: "ambiguous"};
		}
		if (!document.querySelector(".b-support-ticket")) return {state: "operation-drift"};
		if (args.action === "reply") {
			const field = document.querySelector('.b-support-ticket__form-message[name="message"]');
			const submit = document.querySelector(".b-support-ticket__form-submit");
			if (!field || !submit) return {state: "operation-drift"};
			setText(field, args.message);
			dispatched = true;
			submit.click();
			await new Promise(resolve => setTimeout(resolve, 5000));
			const exact = Array.from(document.querySelectorAll(".b-support-ticket__message-text")).filter(node => String(node.textContent || "").trim() === args.message).length;
			return exact === 1 ? {state: "committed"} : {state: "ambiguous"};
		}
		if (args.action === "close") {
			const close = document.querySelector(".b-support-ticket__form-close");
			if (!close) return {state: "operation-drift"};
			close.click();
			const confirm = await waitFor(() => Array.from(document.querySelectorAll("button.b-button_color_primary"))
				.find(button => button.offsetParent !== null && String(button.textContent || "").trim() === "OK"));
			if (!confirm) return {state: "operation-drift"};
			dispatched = true;
			confirm.click();
			await new Promise(resolve => setTimeout(resolve, 5000));
			return document.querySelector(".b-support-ticket__state_color_red") && document.querySelector(".b-support-ticket__message-customer-closed") ? {state: "committed"} : {state: "ambiguous"};
		}
		return {state: "operation-drift"};
	} catch (_) {
		return {state: dispatched ? "ambiguous" : "transport"};
	}
}`
