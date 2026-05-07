#!/usr/bin/env bash
# fake-claude — emit a canned stream-json transcript so integration
# tests don't have to burn real Anthropic tokens. Honors a few env
# vars so the same script covers happy, mid-stream-fail, sleep, and
# echo-stdin scenarios.
#
# Behaviour controlled by FAKE_CLAUDE_MODE:
#   ok        - emit a 3-event happy-path transcript and exit 0
#   midfail   - emit message_start, then exit 1 (failure mid-stream)
#   sleep     - sleep forever (used to test --timeout)
#   echo      - echo stdin into the final text deltas (lets the test
#               assert the prompt reached us)
#   denysig   - install SIGTERM handler that exits 130 to verify Ctrl-C
#               handling
#
# Default: ok.
set -euo pipefail

mode="${FAKE_CLAUDE_MODE:-ok}"

emit() {
    printf '%s\n' "$1"
}

case "$mode" in
    ok)
        emit '{"type":"message_start","message":{"id":"fake-msg-1","model":"fake-haiku","usage":{"input_tokens":42,"output_tokens":0}}}'
        emit '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'
        emit '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}'
        emit '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}'
        emit '{"type":"content_block_stop","index":0}'
        emit '{"type":"message_delta","message":{"usage":{"input_tokens":42,"output_tokens":2}}}'
        emit '{"type":"message_stop"}'
        ;;
    midfail)
        emit '{"type":"message_start","message":{"id":"fake-msg-2"}}'
        emit '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'
        emit '{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"oh no"}}'
        # exit non-zero without message_stop — runner must surface
        # OutcomeChildFailed.
        exit 1
        ;;
    sleep)
        # Read stdin so cmd.Stdin pipe doesn't EPIPE on us, then sleep.
        cat >/dev/null
        sleep 600
        ;;
    echo)
        # Read entire stdin, escape and emit as a text_delta. This lets
        # the test assert the prompt actually reached the child.
        prompt=$(cat)
        # JSON-escape (rudimentary): backslashes + double-quotes + newlines.
        escaped=$(printf '%s' "$prompt" | python3 -c 'import json,sys; sys.stdout.write(json.dumps(sys.stdin.read())[1:-1])' 2>/dev/null || printf '%s' "$prompt" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g')
        emit '{"type":"message_start","message":{"id":"fake-echo"}}'
        emit '{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}'
        emit "{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"${escaped}\"}}"
        emit '{"type":"content_block_stop","index":0}'
        emit '{"type":"message_stop"}'
        ;;
    denysig)
        trap 'exit 130' TERM INT
        cat >/dev/null
        sleep 600 &
        wait
        ;;
    *)
        printf 'fake-claude: unknown mode %q\n' "$mode" >&2
        exit 2
        ;;
esac
