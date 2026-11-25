#!/bin/bash -eu

SCRIPT_DIR=$(dirname "$0")

readonly RENDERER="${SCRIPT_DIR}/render-comment.jq"
readonly TESTDATA_DIR="${SCRIPT_DIR}/testdata"
readonly INVALID_CASE_PREFIX="invalid-"
readonly INPUT_SUFFIX=".json"
readonly EXPECTED_OUTPUT_SUFFIX=".md"
readonly EXPECTED_ERROR_SUFFIX=".err"

function log() {
  local level="$1"
  local message="$2"
  local timestamp=""
  timestamp=$(date '+%Y-%m-%dT%H:%M:%S.%3N%z')
  echo "${timestamp} ${level} : ${message}"
}

function log_info() {
  log "INFO" "$1"
}

function log_error() {
  log "ERROR" "$1"
}

function render() {
  local input_file="$1"
  jq -r -f "${RENDERER}" "${input_file}"
}

# 描画結果が <case>.md と一致すること
function verify_rendered_output() {
  local input_file="$1"
  local expected_file="${input_file%"${INPUT_SUFFIX}"}${EXPECTED_OUTPUT_SUFFIX}"
  local actual=""
  local diff_output=""
  if ! actual=$(render "${input_file}" 2>&1); then
    log_error "render failed: ${input_file}: ${actual}"
    return 1
  fi
  if ! diff_output=$(diff -u "${expected_file}" - <<<"${actual}"); then
    log_error "rendered output differs from ${expected_file}:"
    echo "${diff_output}"
    return 1
  fi
  return 0
}

# 描画が失敗し、エラー出力が <case>.err の文字列を含むこと
function verify_rejected_input() {
  local input_file="$1"
  local expected_error_file="${input_file%"${INPUT_SUFFIX}"}${EXPECTED_ERROR_SUFFIX}"
  local expected_error=""
  local actual=""
  expected_error=$(cat "${expected_error_file}")
  if actual=$(render "${input_file}" 2>&1); then
    log_error "render succeeded but should fail: ${input_file}"
    return 1
  fi
  if ! grep -qF -- "${expected_error}" <<<"${actual}"; then
    log_error "error output does not contain '${expected_error}': ${actual}"
    return 1
  fi
  return 0
}

function verify_case() {
  local input_file="$1"
  local case_name=""
  case_name=$(basename "${input_file}" "${INPUT_SUFFIX}")
  case "${case_name}" in
  "${INVALID_CASE_PREFIX}"*)
    verify_rejected_input "${input_file}"
    ;;
  *)
    verify_rendered_output "${input_file}"
    ;;
  esac
}

function verify_all_cases() {
  local error_count=0
  local input_file=""
  for input_file in "${TESTDATA_DIR}"/*"${INPUT_SUFFIX}"; do
    if verify_case "${input_file}"; then
      log_info "PASS: ${input_file}"
    else
      log_error "FAIL: ${input_file}"
      error_count=$((error_count + 1))
    fi
  done
  if [ "${error_count}" -gt 0 ]; then
    log_error "${error_count} case(s) failed"
    return 1
  fi
  log_info "all cases passed"
  return 0
}

function main() {
  verify_all_cases
}

main
