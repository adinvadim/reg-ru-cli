#!/bin/sh
set -eu

account=
vault=
item=

while test "$#" -gt 0; do
	case "$1" in
	--account)
		account=${2-}
		shift 2
		;;
	--vault)
		vault=${2-}
		shift 2
		;;
	--item)
		item=${2-}
		shift 2
		;;
	*)
		exit 2
		;;
	esac
done

test -n "$account" && test -n "$vault" && test -n "$item"

op item get "$item" \
	--account "$account" \
	--vault "$vault" \
	--format json |
	jq -ce '
		def exact_field($label):
			[.fields[]? | select(.label == $label) | .value |
			 select(type == "string" and length > 0)] |
			if length > 1 then error("duplicate field label") else .[0] // null end;
		{
			schemaVersion: "regru.credential-process/v1",
			fields: ({
				"portal.login": exact_field("portal.login"),
				"portal.password": exact_field("portal.password"),
				"regapi.username": exact_field("regapi.username"),
				"regapi.password": exact_field("regapi.password"),
				"cloudvps.token": exact_field("cloudvps.token"),
				"s3.access_key_id": exact_field("s3.access_key_id"),
				"s3.secret_access_key": exact_field("s3.secret_access_key")
			} | with_entries(select(.value != null)))
		} |
		if (.fields | length) == 0 then error("no supported fields") else . end
	'

