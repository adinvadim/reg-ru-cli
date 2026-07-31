package cdp

// These programs deliberately reduce private GraphQL responses inside the
// isolated browser world. Inventory never selects secret fields; the credential
// program returns one key pair only to its in-process consumer.
const s3InventoryProgram = `async function(args) {
	const endpoint = "https://cloudvps-graphql-server.svc.reg.ru/api";
	const graph = async (query, variables, serviceId) => {
		const headers = {"content-type": "application/json"};
		if (serviceId) headers["Service-ID"] = String(serviceId);
		let response;
		try {
			response = await fetch(endpoint, {
				method: "POST", credentials: "include", headers,
				body: JSON.stringify({query, variables: variables || {}})
			});
		} catch (_) { return {state: "network"}; }
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) return {state: "network", status: response.status};
		const text = await response.text();
		if (text.length > 262144) return {state: "drift"};
		let envelope;
		try { envelope = JSON.parse(text); } catch (_) { return {state: "drift"}; }
		if (!envelope || !envelope.data || (Array.isArray(envelope.errors) && envelope.errors.length)) {
			return {state: "drift"};
		}
		return {state: "ok", data: envelope.data};
	};
	const selectService = async () => {
		if (args && args.serviceId) return {state: "ok", serviceId: String(args.serviceId)};
		const selected = await graph(` + "`" + `query regruS3Environments {
			environments {
				__typename
				... on Environments { environments { serviceId } }
			}
		}` + "`" + `, {}, "");
		if (selected.state !== "ok") return selected;
		const value = selected.data.environments;
		if (!value || value.__typename === "Unauthorized") return {state: "unauthorized"};
		if (value.__typename === "EnvironmentNotFound") return {state: "no-environment"};
		if (value.__typename !== "Environments" || !Array.isArray(value.environments)) return {state: "drift"};
		const ids = value.environments.map(item => item && item.serviceId).filter(Boolean);
		if (ids.length === 0) return {state: "no-environment"};
		if (ids.length !== 1) return {state: "environment-required"};
		return {state: "ok", serviceId: String(ids[0])};
	};
	const selected = await selectService();
	if (selected.state !== "ok") return selected;
	const response = await graph(` + "`" + `query regruS3Inventory {
		objectStore {
			__typename
			... on ObjectStore {
				id name status isLocked createdAt bucketCount bucketLimit size sizeUnit maxQuotaGb quotaGb instanceId
				keypairs { id name instanceId server createdAt }
				buckets { name size sizeUnit quotaGb objectsCount accessType isVersioningEnabled pathStyleLink virtualHostedStyleLink }
			}
			... on ObjectStoreNotFound { message }
			... on Unauthorized { message }
		}
	}` + "`" + `, {}, selected.serviceId);
	if (response.state !== "ok") return response;
	const store = response.data.objectStore;
	if (!store || typeof store.__typename !== "string") return {state: "drift"};
	if (store.__typename === "Unauthorized") return {state: "unauthorized"};
	if (store.__typename === "ObjectStoreNotFound") return {state: "not-configured"};
	if (store.__typename !== "ObjectStore" || !Array.isArray(store.buckets) || !Array.isArray(store.keypairs)) {
		return {state: "drift"};
	}
	return {state: "available", serviceId: selected.serviceId, objectStore: store};
}`

const s3MutationProgram = `async function(args) {
	const endpoint = "https://cloudvps-graphql-server.svc.reg.ru/api";
	const graph = async (query, variables, serviceId, mutating) => {
		const headers = {"content-type": "application/json", "Service-ID": String(serviceId)};
		let response;
		try {
			response = await fetch(endpoint, {
				method: "POST", credentials: "include", headers,
				body: JSON.stringify({query, variables: variables || {}})
			});
		} catch (_) { return {state: mutating ? "ambiguous" : "network"}; }
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) {
			if (mutating && (response.status === 429 || response.status >= 500)) return {state: "ambiguous"};
			return {state: "network", status: response.status};
		}
		const text = await response.text();
		if (text.length > 262144) return {state: mutating ? "ambiguous" : "drift"};
		let envelope;
		try { envelope = JSON.parse(text); } catch (_) { return {state: mutating ? "ambiguous" : "drift"}; }
		if (!envelope || !envelope.data || (Array.isArray(envelope.errors) && envelope.errors.length)) {
			return {state: mutating ? "ambiguous" : "drift"};
		}
		return {state: "ok", data: envelope.data};
	};
	if (!args || !args.serviceId || !args.action) return {state: "drift"};
	const contracts = {
		"bucket.create": ["createBucket", ["name", "objectStoreId", "isPublic", "quotaGb"]],
		"bucket.delete": ["deleteBucket", ["name", "objectStoreId", "force"]],
		"bucket.privacy": ["manageBucketPrivacy", ["name", "objectStoreId", "isPublic"]],
		"bucket.quota": ["manageBucketQuota", ["name", "objectStoreId", "quotaGb"]],
		"service.quota": ["manageObjectStoreQuota", ["objectStoreId", "quotaGb"]],
		"credentials.create": ["createObjectStoreKeyPair", ["name"]],
		"credentials.revoke": ["deleteObjectStoreKeyPair", ["keyPairId"]]
	};
	const contract = contracts[args.action];
	if (!contract) return {state: "drift"};
	const probe = await graph(` + "`" + `query regruS3MutationContract {
		__type(name: "Mutation") { fields(includeDeprecated: true) { name args { name } } }
	}` + "`" + `, {}, args.serviceId, false);
	if (probe.state !== "ok") return probe;
	const fields = probe.data.__type && probe.data.__type.fields;
	if (!Array.isArray(fields)) return {state: "drift"};
	const field = fields.find(item => item && item.name === contract[0]);
	if (!field || !Array.isArray(field.args)) return {state: "drift"};
	const actualArgs = field.args.map(item => item.name).sort().join(",");
	const expectedArgs = contract[1].slice().sort().join(",");
	if (actualArgs !== expectedArgs) return {state: "drift"};
	let query = "";
	let variables = {};
	if (args.action === "bucket.create") {
		query = ` + "`" + `mutation regruCreateBucket($name: String!, $objectStoreId: Int!, $isPublic: Boolean!, $quotaGb: Int = null) {
			createBucket(name: $name, objectStoreId: $objectStoreId, isPublic: $isPublic, quotaGb: $quotaGb) {
				__typename ... on Bucket { name quotaGb accessType objectsCount isVersioningEnabled pathStyleLink virtualHostedStyleLink }
				... on ObjectStoreNotFound { message } ... on InvalidBucketName { message }
				... on BucketNameConflict { message } ... on BucketLimitExceeded { message }
				... on InvalidQuota { message } ... on Unauthorized { message }
			}
		}` + "`" + `;
		variables = {name: args.name, objectStoreId: args.objectStoreId, isPublic: !!args.isPublic, quotaGb: args.quotaGb ?? null};
	} else if (args.action === "bucket.delete") {
		query = ` + "`" + `mutation regruDeleteBucket($name: String!, $objectStoreId: Int!, $force: Boolean! = false) {
			deleteBucket(name: $name, objectStoreId: $objectStoreId, force: $force) {
				__typename ... on Bucket { name } ... on ObjectStoreNotFound { message }
				... on BucketNotFound { message } ... on BucketIsNotEmpty { message }
				... on InvalidBucketName { message } ... on Unauthorized { message }
			}
		}` + "`" + `;
		variables = {name: args.name, objectStoreId: args.objectStoreId, force: false};
	} else if (args.action === "bucket.privacy") {
		query = ` + "`" + `mutation regruManageBucketPrivacy($name: String!, $objectStoreId: Int!, $isPublic: Boolean!) {
			manageBucketPrivacy(name: $name, objectStoreId: $objectStoreId, isPublic: $isPublic) {
				__typename ... on Bucket { name } ... on ObjectStoreNotFound { message }
				... on BucketNotFound { message } ... on InvalidBucketName { message } ... on Unauthorized { message }
			}
		}` + "`" + `;
		variables = {name: args.name, objectStoreId: args.objectStoreId, isPublic: !!args.isPublic};
	} else if (args.action === "bucket.quota") {
		query = ` + "`" + `mutation regruManageBucketQuota($name: String!, $objectStoreId: Int!, $quotaGb: Int) {
			manageBucketQuota(name: $name, objectStoreId: $objectStoreId, quotaGb: $quotaGb) {
				__typename ... on Bucket { name } ... on ObjectStoreNotFound { message }
				... on BucketNotFound { message } ... on InvalidQuota { message }
				... on InvalidBucketName { message } ... on Unauthorized { message }
			}
		}` + "`" + `;
		variables = {name: args.name, objectStoreId: args.objectStoreId, quotaGb: args.quotaGb ?? null};
	} else if (args.action === "service.quota") {
		query = ` + "`" + `mutation regruManageObjectStoreQuota($objectStoreId: Int!, $quotaGb: Int!) {
			manageObjectStoreQuota(objectStoreId: $objectStoreId, quotaGb: $quotaGb) {
				__typename ... on ObjectStore { id quotaGb isLocked } ... on ObjectStoreNotFound { message }
				... on InvalidQuota { message } ... on UnavailableQuota { message } ... on Unauthorized { message }
			}
		}` + "`" + `;
		variables = {objectStoreId: args.objectStoreId, quotaGb: args.quotaGb};
	} else if (args.action === "credentials.create") {
		query = ` + "`" + `mutation regruCreateObjectStoreKeyPair($name: String!) {
			createObjectStoreKeyPair(name: $name) {
				__typename ... on KeyPair { id name instanceId createdAt }
				... on Unauthorized { message } ... on ObjectStoreNotFound { message }
				... on ObjectStoreKeyPairAlreadyExists { message } ... on ObjectStoreKeyPairInvalidName { message }
			}
		}` + "`" + `;
		variables = {name: args.keyName};
	} else if (args.action === "credentials.revoke") {
		query = ` + "`" + `mutation regruDeleteObjectStoreKeyPair($keyPairId: Int!) {
			deleteObjectStoreKeyPair(keyPairId: $keyPairId) {
				__typename ... on KeyPair { id name instanceId createdAt }
				... on Unauthorized { message } ... on ObjectStoreKeyPairNotFound { message }
			}
		}` + "`" + `;
		variables = {keyPairId: args.keyPairId};
	}
	const response = await graph(query, variables, args.serviceId, true);
	if (response.state !== "ok") return response;
	const value = response.data[contract[0]];
	if (!value || typeof value.__typename !== "string") return {state: "ambiguous"};
	return {state: "delivered", typename: value.__typename, result: value};
}`

const s3CredentialsProgram = `async function(args) {
	const endpoint = "https://cloudvps-graphql-server.svc.reg.ru/api";
	const graph = async (query, variables, serviceId) => {
		const headers = {"content-type": "application/json"};
		if (serviceId) headers["Service-ID"] = String(serviceId);
		let response;
		try { response = await fetch(endpoint, {method: "POST", credentials: "include", headers, body: JSON.stringify({query, variables: variables || {}})}); }
		catch (_) { return {state: "network"}; }
		if (response.status === 401 || response.status === 403) return {state: "unauthorized"};
		if (!response.ok) return {state: "network"};
		const text = await response.text();
		if (text.length > 65536) return {state: "drift"};
		let envelope;
		try { envelope = JSON.parse(text); } catch (_) { return {state: "drift"}; }
		if (!envelope || !envelope.data || (Array.isArray(envelope.errors) && envelope.errors.length)) return {state: "drift"};
		return {state: "ok", data: envelope.data};
	};
	let serviceId = args && args.serviceId ? String(args.serviceId) : "";
	if (!serviceId) {
		const selected = await graph(` + "`" + `query regruS3CredentialEnvironments {
			environments { __typename ... on Environments { environments { serviceId } } }
		}` + "`" + `, {}, "");
		if (selected.state !== "ok") return selected;
		const value = selected.data.environments;
		if (!value || value.__typename === "Unauthorized") return {state: "unauthorized"};
		const ids = value.__typename === "Environments" && Array.isArray(value.environments)
			? value.environments.map(item => item && item.serviceId).filter(Boolean) : [];
		if (ids.length === 0) return {state: "no-environment"};
		if (ids.length !== 1) return {state: "environment-required"};
		serviceId = String(ids[0]);
	}
	if (args && args.keyPairId) {
		const selected = await graph(` + "`" + `query regruS3Credential($keyPairId: Int!) {
			objectStoreKeyPair(keyPairId: $keyPairId) {
				__typename ... on KeyPair { id accessKey secretKey }
				... on Unauthorized { message } ... on ObjectStoreNotFound { message }
			}
		}` + "`" + `, {keyPairId: Number(args.keyPairId)}, serviceId);
		if (selected.state !== "ok") return selected;
		const key = selected.data.objectStoreKeyPair;
		if (!key || key.__typename === "Unauthorized") return {state: "unauthorized"};
		if (key.__typename !== "KeyPair" || !key.accessKey || !key.secretKey) return {state: "not-configured"};
		return {state: "available", accessKey: String(key.accessKey), secretKey: String(key.secretKey)};
	}
	const selected = await graph(` + "`" + `query regruS3DefaultCredential {
		objectStore {
			__typename
			... on ObjectStore {
				keypair { id server accessKey secretKey }
				keypairs { id }
			}
			... on ObjectStoreNotFound { message }
			... on Unauthorized { message }
		}
	}` + "`" + `, {}, serviceId);
	if (selected.state !== "ok") return selected;
	const store = selected.data.objectStore;
	if (!store || store.__typename === "Unauthorized") return {state: "unauthorized"};
	if (store.__typename !== "ObjectStore") return {state: "not-configured"};
	if (!Array.isArray(store.keypairs) || store.keypairs.length === 0 || !store.keypair) return {state: "not-configured"};
	if (store.keypairs.length !== 1) return {state: "key-selection-required"};
	if (!store.keypair.accessKey || !store.keypair.secretKey) return {state: "not-configured"};
	return {
		state: "available", endpoint: store.keypair.server || "",
		accessKey: String(store.keypair.accessKey), secretKey: String(store.keypair.secretKey)
	};
}`
