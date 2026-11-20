package craig_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"discord-ai-assistant/internal/voicesummary/craig"
	"discord-ai-assistant/test/craigfake"
)

const (
	deleteKey = "DeleteSecret456"

	targetJob       = craigfake.DeletionTargetJob
	targetRecording = craigfake.DeletionTargetRecording
)

type deleteOutcome struct {
	Err       error
	Deletions []craigfake.Deletion
}

func deleteLink() craig.Link {
	return craig.Link{RecordingID: recordingID, Key: recordingKey, DeleteKey: deleteKey}
}

// newDeleteScenario は、変換済みのジョブがある録音を持つ fake と、その fake に向けた Client を返す。
func newDeleteScenario(t *testing.T) (*craigfake.Server, *craig.Client, string) {
	t.Helper()
	client, fake, srv := newClient(t)
	rec := meeting()
	rec.DeleteKey = deleteKey
	fake.AddRecording(rec)
	fake.SetJob(recordingID, craigfake.Job{Status: "complete", OutputFileName: "output.flac.zip"})
	return fake, client, srv.URL
}

// 条件: 変換済みのジョブがある録音を、削除用のリンクで消す。
// 内容: ジョブ、録音の順に消える。
func TestClientDelete_DeleteLink_DeletesJobThenRecording(t *testing.T) {
	fake, client, _ := newDeleteScenario(t)

	err := client.Delete(t.Context(), deleteLink())

	got := deleteOutcome{Err: err, Deletions: fake.Deletions()}
	want := deleteOutcome{Deletions: []craigfake.Deletion{{RecordingID: recordingID, Target: targetJob}, {RecordingID: recordingID, Target: targetRecording}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Delete() = %+v; want %+v", got, want)
	}
}

// 条件: 既に消した録音を、もう一度消す（Craig は no_job と rec_no_data を返す）。
// 内容: 消し済みは成功として扱い、エラーを返さない。
func TestClientDelete_AlreadyDeleted_ReturnsNil(t *testing.T) {
	_, client, _ := newDeleteScenario(t)
	if err := client.Delete(t.Context(), deleteLink()); err != nil {
		t.Fatalf("first Delete() = %v", err)
	}

	err := client.Delete(t.Context(), deleteLink())

	if err != nil {
		t.Errorf("Delete() = %v; want nil", err)
	}
}

// 条件: Craig が 401 invalid_key を返す。
// 内容: エラーを返し、エラー文に鍵、削除の鍵、URL を入れない。
func TestClientDelete_Rejected_ReturnsErrorWithoutSecrets(t *testing.T) {
	fake, client, serverURL := newDeleteScenario(t)
	fake.FailWith(http.StatusUnauthorized, "invalid_key")

	err := client.Delete(t.Context(), deleteLink())

	if err == nil {
		t.Fatal("Delete() = nil; want error")
	}
	for _, secret := range []string{recordingKey, deleteKey, serverURL} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q contains %q", err, secret)
		}
	}
}
