#!/bin/bash -eu
set -o pipefail

# 使い方: collect-facts.sh <base-ref> <out-dir>
# カレントディレクトリ（PR head のチェックアウト）を head とし、base-ref との間で変わった依存の事実を out-dir に集める。
BASE_REF=$1
OUT_DIR=$2

readonly MODULE_CHANGES_FILE="module-changes.tsv"
readonly MOD_GRAPH_DIFF_FILE="mod-graph.diff"
readonly GO_SUM_DIFF_FILE="go.sum.diff"
readonly SOURCE_DIFF_DIR="source-diff"
readonly AFFECTED_PACKAGES_FILE="affected-packages.tsv"
readonly GO_TEST_FILE="go-test.txt"
readonly TAB=$'\t'
readonly MODULE_CHANGES_HEADER="change${TAB}module${TAB}old${TAB}new"
readonly AFFECTED_PACKAGES_HEADER="module${TAB}package${TAB}build_tags"
readonly CHANGE_ADDED="added"
readonly CHANGE_REMOVED="removed"
readonly CHANGE_VERSION="changed"
readonly INTEGRATION_TAG="integration"
readonly NO_AFFECTED_TESTS_MESSAGE="no affected unit-test packages"
readonly NO_DEPENDENCY_CHANGE_MESSAGE="no go.mod or go.sum change"

WORK_DIR=""

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

function log_warn() {
  log "WARN" "$1"
}

function cleanup() {
  if [ -n "${WORK_DIR}" ]; then
    rm -rf "${WORK_DIR}"
  fi
}

function prepare_out_dir() {
  mkdir -p "${OUT_DIR}/${SOURCE_DIFF_DIR}"
  OUT_DIR=$(cd "${OUT_DIR}" && pwd)
}

function has_dependency_file_changes() {
  ! git diff --quiet "${BASE_REF}" -- go.mod go.sum
}

# go.mod と go.sum が変わらない PR（docker・github-actions など）では、他の成果物も空の状態で揃える
function write_unchanged_outputs() {
  echo "${MODULE_CHANGES_HEADER}" >"${OUT_DIR}/${MODULE_CHANGES_FILE}"
  : >"${OUT_DIR}/${MOD_GRAPH_DIFF_FILE}"
  : >"${OUT_DIR}/${GO_SUM_DIFF_FILE}"
  echo "${AFFECTED_PACKAGES_HEADER}" >"${OUT_DIR}/${AFFECTED_PACKAGES_FILE}"
  echo "${NO_AFFECTED_TESTS_MESSAGE}" >"${OUT_DIR}/${GO_TEST_FILE}"
  log_info "${NO_DEPENDENCY_CHANGE_MESSAGE}"
}

# base の go.mod と go.sum だけを一時ディレクトリに書き出す（ソースのチェックアウトは不要）
function prepare_base_module() {
  WORK_DIR=$(mktemp -d)
  mkdir -p "${WORK_DIR}/base"
  git show "${BASE_REF}:go.mod" >"${WORK_DIR}/base/go.mod"
  git show "${BASE_REF}:go.sum" >"${WORK_DIR}/base/go.sum"
}

# 「モジュールパス<TAB>版」をソートして出す。main モジュール（版なし）は除く
function list_modules() {
  local module_dir="$1"
  (cd "${module_dir}" && go list -m -f '{{.Path}} {{.Version}}' all) |
    awk -v OFS="${TAB}" 'NF >= 2 { print $1, $2 }' | sort
}

function collect_module_changes() {
  local base_modules="${WORK_DIR}/base-modules.tsv"
  local head_modules="${WORK_DIR}/head-modules.tsv"
  list_modules "${WORK_DIR}/base" >"${base_modules}"
  list_modules "." >"${head_modules}"
  {
    echo "${MODULE_CHANGES_HEADER}"
    awk -F "${TAB}" -v OFS="${TAB}" \
      -v added="${CHANGE_ADDED}" -v removed="${CHANGE_REMOVED}" -v changed="${CHANGE_VERSION}" '
      NR == FNR { old[$1] = $2; next }
      { new[$1] = $2 }
      END {
        for (m in old) {
          if (!(m in new)) print removed, m, old[m], ""
          else if (old[m] != new[m]) print changed, m, old[m], new[m]
        }
        for (m in new) {
          if (!(m in old)) print added, m, "", new[m]
        }
      }' "${base_modules}" "${head_modules}" | sort -t "${TAB}" -k2,2
  } >"${OUT_DIR}/${MODULE_CHANGES_FILE}"
  log_info "wrote ${MODULE_CHANGES_FILE}"
}

# diff は差分があると 1 を返すので、1 は成功として扱う
function write_unified_diff() {
  local before="$1"
  local after="$2"
  local output_file="$3"
  local status=0
  diff -u "${before}" "${after}" >"${output_file}" || status=$?
  [ "${status}" -le 1 ]
}

function collect_mod_graph_diff() {
  (cd "${WORK_DIR}/base" && go mod graph | sort) >"${WORK_DIR}/base-graph.txt"
  go mod graph | sort >"${WORK_DIR}/head-graph.txt"
  write_unified_diff "${WORK_DIR}/base-graph.txt" "${WORK_DIR}/head-graph.txt" "${OUT_DIR}/${MOD_GRAPH_DIFF_FILE}"
  log_info "wrote ${MOD_GRAPH_DIFF_FILE}"
}

function collect_go_sum_diff() {
  git diff "${BASE_REF}" -- go.sum >"${OUT_DIR}/${GO_SUM_DIFF_FILE}"
  log_info "wrote ${GO_SUM_DIFF_FILE}"
}

# 版が変わったモジュールの「モジュールパス<TAB>旧版<TAB>新版」
function list_version_changed_modules() {
  awk -F "${TAB}" -v OFS="${TAB}" -v changed="${CHANGE_VERSION}" \
    '$1 == changed { print $2, $3, $4 }' "${OUT_DIR}/${MODULE_CHANGES_FILE}"
}

# 変わったモジュール名（追加を含み、削除を除く）
function list_changed_module_names() {
  awk -F "${TAB}" -v removed="${CHANGE_REMOVED}" 'NR > 1 && $1 != removed { print $2 }' \
    "${OUT_DIR}/${MODULE_CHANGES_FILE}"
}

# このリポジトリから到達する、変わったモジュールのパッケージ（「モジュールパス<TAB>import path」）
function list_used_packages() {
  local build_tags=""
  for build_tags in "" "${INTEGRATION_TAG}"; do
    go list -tags "${build_tags}" -deps -test -f '{{with .Module}}{{.Path}}{{"\t"}}{{$.ImportPath}}{{end}}' ./...
  done | awk -F "${TAB}" -v OFS="${TAB}" 'NF == 2 { sub(/ .*/, "", $2); print $1, $2 }' | sort -u
}

# import path のモジュールルートからの相対ディレクトリ
function relative_package_dir() {
  local module="$1"
  local package="$2"
  local relative="${package#"${module}"}"
  relative="${relative#/}"
  echo "${relative:-.}"
}

# 2 つのディレクトリの直下にある、_test.go 以外のファイル名の和集合（ディレクトリがなければ空）
function list_package_files() {
  local old_package_dir="$1"
  local new_package_dir="$2"
  {
    find "${old_package_dir}" -maxdepth 1 -type f -printf '%f\n' 2>/dev/null || true
    find "${new_package_dir}" -maxdepth 1 -type f -printf '%f\n' 2>/dev/null || true
  } | grep -v '_test\.go$' | sort -u || true
}

# パッケージのディレクトリ直下（非再帰）のファイルだけを diff する。パッケージが片方にしかなければ空として扱う
function write_package_diff() {
  local link_dir="$1"
  local relative_dir="$2"
  local status=0
  local file=""
  local path_prefix="${relative_dir}/"
  if [ "${relative_dir}" = "." ]; then
    path_prefix=""
  fi
  while read -r file; do
    (cd "${link_dir}" && diff -uN -- "old/${path_prefix}${file}" "new/${path_prefix}${file}") || status=$?
    [ "${status}" -le 1 ]
    status=0
  done < <(list_package_files "${link_dir}/old/${relative_dir}" "${link_dir}/new/${relative_dir}")
}

# モジュールキャッシュの実パスが差分の見出しに出ないよう、old/new のシンボリックリンク越しに diff を取る
# .diff はこのリポジトリが使うパッケージだけ、.files は使っていない部分も含むモジュール全体の変更ファイル一覧
function write_source_diff() {
  local module="$1"
  local old_version="$2"
  local new_version="$3"
  local output_base="${OUT_DIR}/${SOURCE_DIFF_DIR}/${module//\//_}"
  local download_json=""
  local old_dir=""
  local new_dir=""
  local link_dir=""
  local package=""
  local status=0
  if ! download_json=$(go mod download -json "${module}@${old_version}" "${module}@${new_version}"); then
    log_warn "failed to download ${module}: ${download_json}"
    return 0
  fi
  old_dir=$(jq -rs --arg v "${old_version}" '.[] | select(.Version == $v) | .Dir' <<<"${download_json}")
  new_dir=$(jq -rs --arg v "${new_version}" '.[] | select(.Version == $v) | .Dir' <<<"${download_json}")
  link_dir=$(mktemp -d -p "${WORK_DIR}")
  ln -s "${old_dir}" "${link_dir}/old"
  ln -s "${new_dir}" "${link_dir}/new"
  (cd "${link_dir}" && LC_ALL=C diff -rqN -x '*_test.go' -x 'testdata' old new) >"${output_base}.files" || status=$?
  [ "${status}" -le 1 ]
  : >"${output_base}.diff"
  while read -r package; do
    write_package_diff "${link_dir}" "$(relative_package_dir "${module}" "${package}")" >>"${output_base}.diff"
  done < <(awk -F "${TAB}" -v module="${module}" '$1 == module { print $2 }' "${WORK_DIR}/used-packages.tsv")
  log_info "wrote ${output_base}.diff and ${output_base}.files"
}

function collect_source_diffs() {
  local module=""
  local old_version=""
  local new_version=""
  list_used_packages >"${WORK_DIR}/used-packages.tsv"
  while IFS="${TAB}" read -r module old_version new_version; do
    write_source_diff "${module}" "${old_version}" "${new_version}"
  done < <(list_version_changed_modules)
}

# build_tags が空なら素のパッケージ一覧、あればそのタグでだけ現れるパッケージ（素の一覧に含まれるものは除く）
function list_packages() {
  local build_tags="$1"
  if [ -z "${build_tags}" ]; then
    go list ./...
  else
    comm -13 <(go list ./... | sort) <(go list -tags "${build_tags}" ./... | sort)
  fi
}

function list_dependency_modules() {
  local package="$1"
  local build_tags="$2"
  go list -tags "${build_tags}" -test -deps -f '{{with .Module}}{{.Path}}{{end}}' "${package}" | sort -u
}

function append_affected_packages() {
  local build_tags="$1"
  local changed_modules="$2"
  local package=""
  local module=""
  while read -r package; do
    while read -r module; do
      if grep -qxF -- "${module}" <<<"${changed_modules}"; then
        echo "${module}${TAB}${package}${TAB}${build_tags}"
      fi
    done < <(list_dependency_modules "${package}" "${build_tags}")
  done < <(list_packages "${build_tags}")
}

function collect_affected_packages() {
  local changed_modules=""
  changed_modules=$(list_changed_module_names)
  {
    echo "${AFFECTED_PACKAGES_HEADER}"
    append_affected_packages "" "${changed_modules}"
    append_affected_packages "${INTEGRATION_TAG}" "${changed_modules}"
  } >"${OUT_DIR}/${AFFECTED_PACKAGES_FILE}"
  log_info "wrote ${AFFECTED_PACKAGES_FILE}"
}

# build_tags が空のパッケージだけを実行する。integration は Docker が要るので CI の make docker-test-it に任せる
# テストが失敗しても結果を記録するだけで、このスクリプトは失敗させない
function run_unit_tests() {
  local output_file="${OUT_DIR}/${GO_TEST_FILE}"
  local packages=()
  local exit_code=0
  mapfile -t packages < <(awk -F "${TAB}" 'NR > 1 && $3 == "" { print $2 }' "${OUT_DIR}/${AFFECTED_PACKAGES_FILE}" | sort -u)
  if [ "${#packages[@]}" -eq 0 ]; then
    echo "${NO_AFFECTED_TESTS_MESSAGE}" >"${output_file}"
    return 0
  fi
  if go test -count=1 "${packages[@]}" >"${output_file}" 2>&1; then
    exit_code=0
  else
    exit_code=$?
  fi
  echo "exit code: ${exit_code}" >>"${output_file}"
  log_info "wrote ${GO_TEST_FILE} (exit code: ${exit_code})"
}

function main() {
  trap cleanup EXIT
  prepare_out_dir
  if ! has_dependency_file_changes; then
    write_unchanged_outputs
    return 0
  fi
  prepare_base_module
  collect_module_changes
  collect_mod_graph_diff
  collect_go_sum_diff
  collect_source_diffs
  collect_affected_packages
  run_unit_tests
}

main
