#!/bin/sh
# The hook protocol carries Vault passwords as base64, never as command arguments.
request=$(cat)
field() {
    printf '%s' "$request" | /usr/bin/plutil -extract "$1" raw -expect "$2" -o - - 2>/dev/null
}
unavailable() {
    printf '%s\n' '{"status":"unavailable"}'
    exit 0
}

version=$(field version integer) || unavailable
action=$(field action string) || unavailable
service=$(field service string) || unavailable
account=$(field account string) || unavailable
[ "$version" = 1 ] && [ "$service" = 'AACT shared Vault' ] || unavailable
case "$account" in state-*) account_id=${account#state-} ;; *) unavailable ;; esac
[ "${#account_id}" -eq 64 ] || unavailable
case "$account_id" in *[!0123456789abcdef]*) unavailable ;; esac

case "$action" in
    get)
        if secret=$(/usr/bin/security find-generic-password -s "$service" -a "$account" -w 2>/dev/null); then
            case "$secret" in ''|*[!A-Za-z0-9+/=]*) unavailable ;; esac
            printf '{"status":"ok","secret":"%s"}\n' "$secret"
        else
            printf '%s\n' '{"status":"missing"}'
        fi
        ;;
    set)
        secret=$(field secret string) || unavailable
        case "$secret" in ''|*[!A-Za-z0-9+/=]*) unavailable ;; esac
        if printf 'add-generic-password -U -s "%s" -a "%s" -w "%s"\n' "$service" "$account" "$secret" |
            /usr/bin/security -i >/dev/null 2>&1; then
            printf '%s\n' '{"status":"ok"}'
        else
            unavailable
        fi
        ;;
    *) unavailable ;;
esac
