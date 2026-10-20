import hashlib
import json
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest
from jsonschema.validators import validator_for

from threadforge_py.protocol import ProtocolError, dispatch
from threadforge_py import distillation as distillation_module
from threadforge_py import protocol as protocol_module


def _cache_fixture(tmp_path: Path) -> tuple[Path, dict]:
    root = tmp_path / "cache-root"
    root.mkdir(parents=True)
    rng = np.random.RandomState(9)
    train_y = np.arange(300, dtype=np.int64) % 2
    train_h = rng.normal(0, 0.1, (300, 1, 4)).astype(np.float32)
    train_h[:, 0, 0] += np.where(train_y == 1, 1.0, -1.0)
    test_y = np.arange(12, dtype=np.int64) % 2
    test_h = rng.normal(0, 0.1, (12, 1, 4)).astype(np.float32)
    test_h[:, 0, 0] += np.where(test_y == 1, 1.0, -1.0)
    synth_y = np.arange(40, dtype=np.int64) % 2
    synth_h = rng.normal(0, 0.1, (40, 4)).astype(np.float32)
    synth_h[:, 0] += np.where(synth_y == 1, 1.0, -1.0)
    teacher_synth = np.full((40, 2), 0.1, dtype=np.float32)
    teacher_synth[np.arange(40), synth_y] = 0.9
    teacher_test = np.full((12, 2), 0.1, dtype=np.float32)
    teacher_test[np.arange(12), test_y] = 0.9

    np.savez(root / "teacher-synth.npz", probs=teacher_synth, K=2, block=4)
    np.savez(root / "teacher-test.npz", probs=teacher_test, K=2, block=4, y_gold=test_y)
    layers = np.array([4], dtype=np.int64)
    cache_fields = {"K": 2, "layers": layers, "kind": "choice", "group": "fixture", "name": "fixture"}
    np.savez(root / "gold-train.npz", H=train_h, y=train_y, **cache_fields)
    np.savez(root / "gold-test.npz", H=test_h, y=test_y, **cache_fields)
    np.savez(root / "synth-features.npz", H=synth_h.astype(np.float16), block=4, K=2)
    np.savez(root / "synth-mismatch.npz", H=synth_h[:-1].astype(np.float16), block=4, K=2)
    request = {
        "cache_root": str(root),
        "teacher_model": "fixture/private-teacher",
        "budgets": [10, 20],
        "bootstrap_resamples": 100,
        "questions": [{
            "question_id": "typed.private-workflow.private-question",
            "teacher_synth_path": "teacher-synth.npz",
            "teacher_test_path": "teacher-test.npz",
            "students": [{
                "model": "fixture/private-student",
                "block": 4,
                "n_layers": 4,
                "gold_train_path": "gold-train.npz",
                "gold_test_path": "gold-test.npz",
                "synth_features_path": "synth-features.npz",
            }],
        }],
    }
    return root, request


def test_distillation_fit_replays_hard_soft_and_mixed_targets_without_exposing_inputs(tmp_path):
    root, params = _cache_fixture(tmp_path)
    before = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in root.iterdir()}
    result = dispatch({
        "schema_version": "threadforge.python.v1",
        "request_id": "distill-fit-test",
        "operation": "decision.distill.fit",
        "params": params,
    })["result"]

    assert result["schema_version"] == "threadforge.anyjev-distillation-fit-result.v1"
    assert result["question_count"] == 1
    assert result["total_test_examples"] == 12
    assert result["teacher_sha256"] == hashlib.sha256(b"fixture/private-teacher").hexdigest()
    assert result["students"][0]["student_sha256"] == hashlib.sha256(b"fixture/private-student").hexdigest()
    methods = result["students"][0]["methods"]
    assert {"teacher", "gold300", "synth10-hard", "synth10-soft", "gold300+synth20-hard", "gold300+synth20-soft"} <= set(methods)
    assert methods["synth10-hard"]["examples"] == 12
    assert len(methods["gold300"]["accuracy_ci95"]) == 2
    assert len(methods["gold300+synth10-soft"]["diff_vs_gold300_ci95"]) == 2
    assert result["human_review_required"] is True
    assert result["model_inference_verified"] is False
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    rendered = json.dumps(result)
    for private in ("fixture/private-teacher", "fixture/private-student", params["questions"][0]["question_id"], str(root)):
        assert private not in rendered
    after = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in root.iterdir()}
    assert before == after

    schema_dir = Path(__file__).resolve().parents[2] / "schema"
    request_schema = json.loads((schema_dir / "anyjev-distillation-fit-request-v1.schema.json").read_text(encoding="utf-8"))
    validator_for(request_schema).check_schema(request_schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-fit-request.v1", **params,
    })
    schema = json.loads((schema_dir / "anyjev-distillation-fit-result-v1.schema.json").read_text(encoding="utf-8"))
    validator_for(schema).check_schema(schema)
    validator_for(schema)(schema).validate(result)


def test_distillation_fit_rejects_misaligned_or_unsafe_cache_paths(tmp_path):
    _root, params = _cache_fixture(tmp_path)
    unsafe = json.loads(json.dumps(params))
    unsafe["questions"][0]["teacher_synth_path"] = "../outside.npz"
    with pytest.raises(ProtocolError, match="relative|component"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.fit", "params": unsafe})

    mismatch = json.loads(json.dumps(params))
    mismatch["questions"][0]["students"][0]["synth_features_path"] = "synth-mismatch.npz"
    with pytest.raises(ProtocolError, match="align exactly"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.fit", "params": mismatch})


def test_distillation_fit_rejects_credentials_and_untrusted_numpy_objects(tmp_path):
    root, params = _cache_fixture(tmp_path)
    params["teacher_model"] = "API_TOKEN=do-not-read"
    with pytest.raises(ProtocolError, match="credential"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.fit", "params": params})

    root, params = _cache_fixture(tmp_path / "object")
    object_path = root / "teacher-synth.npz"
    np.savez(object_path, probs=np.array([[object()], [object()]], dtype=object), K=2, block=4)
    with pytest.raises(ProtocolError, match="unsafe array"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.fit", "params": params})


def test_distillation_soft_replays_oof_teacher_targets_against_student_features(tmp_path):
    root = tmp_path / "soft-cache"
    root.mkdir()
    rng = np.random.RandomState(4)
    train_y = np.arange(40, dtype=np.int64) % 2
    test_y = np.arange(12, dtype=np.int64) % 2

    def features(labels, noise):
        values = rng.normal(0, noise, (len(labels), 1, 4)).astype(np.float32)
        values[:, 0, 0] += np.where(labels == 1, 1.0, -1.0)
        return values

    shared = {"K": 2, "layers": np.array([4], dtype=np.int64), "kind": "choice", "group": "local", "name": "local"}
    np.savez(root / "teacher-train.npz", H=features(train_y, 0.2), y=train_y, **shared)
    np.savez(root / "teacher-test.npz", H=features(test_y, 0.2), y=test_y, **shared)
    np.savez(root / "student-train.npz", H=features(train_y, 0.3), y=train_y, **shared)
    np.savez(root / "student-test.npz", H=features(test_y, 0.3), y=test_y, **shared)
    request_params = {
        "cache_root": str(root),
        "teacher_model": "private/teacher-model",
        "teacher_block": 4,
        "teacher_n_layers": 4,
        "bootstrap_resamples": 100,
        "questions": [{
            "question_id": "private/workflow/question",
            "teacher_train_path": "teacher-train.npz",
            "teacher_test_path": "teacher-test.npz",
            "students": [{
                "model": "private/student-model", "block": 4, "n_layers": 4,
                "gold_train_path": "student-train.npz", "gold_test_path": "student-test.npz",
            }],
        }],
    }
    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.soft", "params": request_params,
    })["result"]
    assert result["schema_version"] == "threadforge.anyjev-distillation-soft-result.v1"
    methods = result["students"][0]["methods"]
    assert {"teacher", "gold", "teacher-argmax", "teacher-soft", "gold+teacher-soft"} <= set(methods)
    assert methods["teacher-soft"]["examples"] == 12
    assert len(methods["gold+teacher-soft"]["diff_vs_gold_ci95"]) == 2
    assert result["per_question"][0]["teacher_oof_agreement_with_gold"] >= 0
    assert result["human_review_required"] is True
    assert result["model_inference_verified"] is False
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    rendered = json.dumps(result)
    for private in ("private/teacher-model", "private/student-model", "private/workflow/question", str(root)):
        assert private not in rendered

    schema_dir = Path(__file__).resolve().parents[2] / "schema"
    request_schema = json.loads((schema_dir / "anyjev-distillation-soft-request-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((schema_dir / "anyjev-distillation-soft-result-v1.schema.json").read_text(encoding="utf-8"))
    validator_for(request_schema).check_schema(request_schema)
    validator_for(result_schema).check_schema(result_schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-soft-request.v1", **request_params,
    })
    validator_for(result_schema)(result_schema).validate(result)


def test_distillation_soft_requires_matching_train_and_test_labels(tmp_path):
    root = tmp_path / "soft-mismatch"
    root.mkdir()
    labels = np.arange(40, dtype=np.int64) % 2
    test_labels = (np.arange(12, dtype=np.int64) + 1) % 2
    hidden_train = np.zeros((40, 1, 4), dtype=np.float32)
    hidden_test = np.zeros((12, 1, 4), dtype=np.float32)
    shared = {"K": 2, "layers": np.array([4], dtype=np.int64), "kind": "choice", "group": "fixture", "name": "fixture"}
    np.savez(root / "teacher-train.npz", H=hidden_train, y=labels, **shared)
    np.savez(root / "teacher-test.npz", H=hidden_test, y=test_labels, **shared)
    np.savez(root / "student-train.npz", H=hidden_train, y=labels, **shared)
    np.savez(root / "student-test.npz", H=hidden_test, y=labels[:12], **shared)
    params = {
        "cache_root": str(root), "teacher_model": "local-teacher", "teacher_block": 4, "teacher_n_layers": 4,
        "questions": [{"question_id": "q", "teacher_train_path": "teacher-train.npz",
                       "teacher_test_path": "teacher-test.npz",
                       "students": [{"model": "local-student", "block": 4, "n_layers": 4,
                                     "gold_train_path": "student-train.npz", "gold_test_path": "student-test.npz"}]}],
    }
    with pytest.raises(ProtocolError, match="labels/classes do not align"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.soft", "params": params})


def _label_fixture(tmp_path: Path) -> tuple[dict, Path, Path]:
    data_root = tmp_path / "data"
    cache_root = tmp_path / "cache"
    model_path = tmp_path / "model"
    data_root.mkdir()
    cache_root.mkdir()
    model_path.mkdir()
    (model_path / "config.json").write_text('{"model":"fixture"}', encoding="utf-8")
    (data_root / "workflow.json").write_text(json.dumps({
        "workflow": "fixture", "generator": "local", "n": 2,
        "states": [{"case": "synthetic-a"}, {"case": "synthetic-b"}],
    }), encoding="utf-8")
    question_id = "typed.fixture.action"
    (data_root / "test-cases.json").write_text(json.dumps({
        "schema_version": "threadforge.anyjev-distillation-test-cases.v1",
        "question_id": question_id,
        "question": {"kind": "choice", "text": "Choose an action", "options": ["review", "close"]},
        "examples": [{"state": {"case": "heldout-a"}, "gold_index": 0},
                     {"state": {"case": "heldout-b"}, "gold_index": 1}],
    }), encoding="utf-8")
    checkpoint_sha = protocol_module._fingerprint_model_directory(str(model_path))["sha256"]
    request = {
        "data_root": str(data_root),
        "cache_root": str(cache_root),
        "teacher_model": "fixture/teacher",
        "teacher_model_checkpoint_sha256": checkpoint_sha,
        "backend": {"type": "hf", "model_path": str(model_path), "device": "cpu", "dtype": "float32"},
        "teacher_artifact": {"model": "fixture/teacher", "artifacts": {}, "heads": {}},
        "workflows": [{"workflow": "fixture", "synthetic_states_path": "workflow.json"}],
        "questions": [{"question_id": question_id, "workflow": "fixture", "test_cases_path": "test-cases.json"}],
    }
    return request, cache_root, data_root


def _stub_distillation_model(monkeypatch):
    feature_calls = []

    class FakeDecider:
        def __init__(self, backend, adapt=False):
            assert adapt is False
            self.backend = backend

        def load_artifacts(self, artifact):
            assert artifact["model"] == "fixture/teacher"
            return 1

        def decide_batch(self, states, question, level):
            assert level == "L2"
            return [SimpleNamespace(
                probs=np.asarray([0.75, 0.25], dtype=np.float32),
                diagnostics={"exit_layer": 3},
            ) for _ in states]

        def _features(self, question, state_texts, layers, perms):
            assert layers == [3]
            feature_calls.append((question.ordered, list(state_texts), perms))
            if not question.ordered and perms is not None:
                assert len(perms) == len(state_texts)
                assert all(sorted(permutation) == list(range(question.k)) for permutation in perms)
            return np.ones((len(state_texts), len(layers), 3), dtype=np.float32)

    def local_backend(config, expected_model_name=None, expected_model_checkpoint_sha256=None):
        assert config["type"] == "hf"
        return SimpleNamespace(
            name=expected_model_name,
            _threadforge_model_environment_verified=True,
            _threadforge_model_checkpoint_sha256=expected_model_checkpoint_sha256,
        )

    monkeypatch.setattr(protocol_module, "_backend", local_backend)
    monkeypatch.setattr(protocol_module, "validate_decision_bundle", lambda _params: {})
    monkeypatch.setattr("anyjev.Decider", FakeDecider)
    return feature_calls


def test_distillation_label_creates_private_checkpoint_bound_caches_and_is_idempotent(monkeypatch, tmp_path):
    params, cache_root, data_root = _label_fixture(tmp_path)
    _stub_distillation_model(monkeypatch)
    request_schema = json.loads((Path(__file__).resolve().parents[2] / "schema" /
                                 "anyjev-distillation-label-request-v1.schema.json").read_text(encoding="utf-8"))
    test_schema = json.loads((Path(__file__).resolve().parents[2] / "schema" /
                              "anyjev-distillation-test-cases-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((Path(__file__).resolve().parents[2] / "schema" /
                                "anyjev-distillation-label-result-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, test_schema, result_schema):
        validator_for(schema).check_schema(schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-label-request.v1", **params,
    })
    test_document = json.loads((data_root / "test-cases.json").read_text(encoding="utf-8"))
    validator_for(test_schema)(test_schema).validate(test_document)

    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.label", "params": params,
    })["result"]
    validator_for(result_schema)(result_schema).validate(result)
    assert result["question_count"] == 1
    assert result["total_synthetic_examples"] == 2
    assert result["total_test_examples"] == 2
    assert result["model_inference_verified"] is True
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    assert len(result["caches"]) == 2
    rendered = json.dumps(result)
    for private in ("fixture/teacher", "typed.fixture.action", str(cache_root), str(data_root), "synthetic-a", "heldout-a"):
        assert private not in rendered

    by_role = {cache["role"]: cache for cache in result["caches"]}
    assert set(by_role) == {"teacher_synthetic_probabilities", "teacher_test_probabilities"}
    synth_path = cache_root / by_role["teacher_synthetic_probabilities"]["relative_path"]
    test_path = cache_root / by_role["teacher_test_probabilities"]["relative_path"]
    with np.load(synth_path, allow_pickle=False) as values:
        assert set(values.files) == {"probs", "K", "block"}
        assert values["probs"].shape == (2, 2)
        assert int(values["block"]) == 3
    with np.load(test_path, allow_pickle=False) as values:
        assert set(values.files) == {"probs", "K", "block", "y_gold"}
        assert values["y_gold"].tolist() == [0, 1]

    repeated = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.label", "params": params,
    })["result"]
    assert [cache["sha256"] for cache in repeated["caches"]] == [cache["sha256"] for cache in result["caches"]]


def test_distillation_label_rejects_changed_cache_and_credential_bearing_source(monkeypatch, tmp_path):
    params, cache_root, data_root = _label_fixture(tmp_path)
    _stub_distillation_model(monkeypatch)
    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.label", "params": params,
    })["result"]
    first_cache = cache_root / result["caches"][0]["relative_path"]
    np.savez_compressed(first_cache, probs=np.asarray([[0.25, 0.75], [0.25, 0.75]], dtype=np.float32),
                        K=np.asarray(2), block=np.asarray(3))
    with pytest.raises(ProtocolError, match="conflicts with regenerated output"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.label", "params": params})

    (data_root / "workflow.json").write_text(json.dumps({
        "workflow": "fixture", "states": [{"api_key": "not-a-real-key"}],
    }), encoding="utf-8")
    params["cache_root"] = str(tmp_path / "unwritten-cache")
    (tmp_path / "unwritten-cache").mkdir()
    with pytest.raises(ProtocolError, match="credential"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.label", "params": params})
    assert not any((tmp_path / "unwritten-cache").rglob("*.npz"))


def test_distillation_extract_creates_private_random_order_feature_caches(monkeypatch, tmp_path):
    label_params, cache_root, data_root = _label_fixture(tmp_path)
    _stub_distillation_model(monkeypatch)
    params = {
        "data_root": str(data_root),
        "cache_root": str(cache_root),
        "student_model": "fixture/student",
        "student_model_checkpoint_sha256": label_params["teacher_model_checkpoint_sha256"],
        "backend": {"type": "hf", "model_path": label_params["backend"]["model_path"], "device": "cpu"},
        "block": 3,
        "seed": 17,
        "workflows": [{"workflow": "fixture", "synthetic_states_path": "workflow.json"}],
        "questions": [{
            "question_id": "typed.fixture.action",
            "workflow": "fixture",
            "question": {"kind": "choice", "text": "Choose an action", "options": ["review", "close"]},
        }],
    }
    request_schema = json.loads((Path(__file__).resolve().parents[2] / "schema" /
                                 "anyjev-distillation-extract-request-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((Path(__file__).resolve().parents[2] / "schema" /
                                "anyjev-distillation-extract-result-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, result_schema):
        validator_for(schema).check_schema(schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-extract-request.v1", **params,
    })
    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.extract", "params": params,
    })["result"]
    validator_for(result_schema)(result_schema).validate(result)
    assert result["question_count"] == 1
    assert result["total_synthetic_examples"] == 2
    assert result["model_inference_verified"] is True
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    cache = result["caches"][0]
    assert cache["role"] == "student_synthetic_features"
    assert cache["feature_width"] == 3
    rendered = json.dumps(result)
    for private in ("fixture/student", "typed.fixture.action", str(cache_root), str(data_root), "synthetic-a"):
        assert private not in rendered
    with np.load(cache_root / cache["relative_path"], allow_pickle=False) as values:
        assert set(values.files) == {"H", "block", "K"}
        assert values["H"].dtype == np.float16
        assert values["H"].shape == (2, 3)
        assert int(values["block"]) == 3
        assert int(values["K"]) == 2


def _gold_feature_fixture(tmp_path: Path) -> tuple[dict, Path, Path, Path]:
    data_root = tmp_path / "gold-data"
    cache_root = tmp_path / "gold-cache"
    model_path = tmp_path / "gold-model"
    for path in (data_root, cache_root, model_path):
        path.mkdir(parents=True)
    (model_path / "config.json").write_text('{"model":"fixture"}', encoding="utf-8")
    choice = {"kind": "choice", "text": "Choose an action", "options": ["review", "close"]}
    score = {"kind": "score", "text": "Risk level", "levels": ["low", "medium", "high"]}

    def write_cases(name: str, schema_version: str, question_id: str, spec: dict, indices: list[int]) -> str:
        relative = f"{name}.json"
        (data_root / relative).write_text(json.dumps({
            "schema_version": schema_version,
            "question_id": question_id,
            "question": spec,
            "examples": [
                {"state": {"case": f"private-{name}-{index}"}, "gold_index": label}
                for index, label in enumerate(indices)
            ],
        }), encoding="utf-8")
        return relative

    questions = []
    for suffix, spec, labels in (
        ("action", choice, [0, 1, 0]),
        ("risk", score, [0, 2, 1]),
    ):
        question_id = f"typed.customer_service.{suffix}"
        questions.append({
            "question_id": question_id,
            "workflow": "customer_service",
            "question": spec,
            "train_cases_path": write_cases(
                f"{suffix}-train", "threadforge.anyjev-distillation-train-cases.v1", question_id, spec, labels,
            ),
            "test_cases_path": write_cases(
                f"{suffix}-test", "threadforge.anyjev-distillation-test-cases.v1", question_id, spec, labels[:2],
            ),
        })
    checkpoint_sha = protocol_module._fingerprint_model_directory(str(model_path))["sha256"]
    params = {
        "data_root": str(data_root),
        "cache_root": str(cache_root),
        "model_role": "teacher",
        "model": "fixture/gold-model",
        "model_checkpoint_sha256": checkpoint_sha,
        "backend": {"type": "hf", "model_path": str(model_path), "device": "cpu", "dtype": "float32"},
        "block": 3,
        "seed": 17,
        "questions": questions,
    }
    return params, data_root, cache_root, model_path


def test_distillation_extract_gold_creates_fit_compatible_train_and_test_caches(monkeypatch, tmp_path):
    params, data_root, cache_root, _model_path = _gold_feature_fixture(tmp_path)
    feature_calls = _stub_distillation_model(monkeypatch)
    schema_dir = Path(__file__).resolve().parents[2] / "schema"
    request_schema = json.loads((schema_dir / "anyjev-distillation-extract-gold-request-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((schema_dir / "anyjev-distillation-extract-gold-result-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, result_schema):
        validator_for(schema).check_schema(schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-extract-gold-request.v1", **params,
    })

    result = dispatch({
        "schema_version": "threadforge.python.v1",
        "operation": "decision.distill.extract_gold",
        "params": params,
    })["result"]
    validator_for(result_schema)(result_schema).validate(result)
    assert result["model_role"] == "teacher"
    assert result["question_count"] == 2
    assert result["total_train_examples"] == 6
    assert result["total_test_examples"] == 4
    assert result["cache_count"] == 4
    assert result["model_inference_verified"] is True
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    assert [call[0] for call in feature_calls] == [False, False, True, True]
    assert feature_calls[0][2] is not None and len(feature_calls[0][2]) == 3
    assert feature_calls[1][2] is None
    assert feature_calls[2][2] is None and feature_calls[3][2] is None
    rendered = json.dumps(result)
    for private in ("fixture/gold-model", str(data_root), str(cache_root), "private-action", "private-risk"):
        assert private not in rendered

    from threadforge_py.distillation import _feature_cache

    by_question_split = {(item["question_sha256"], item["split"]): item for item in result["caches"]}
    for question in params["questions"]:
        question_sha = hashlib.sha256(question["question_id"].encode()).hexdigest()
        for split in ("train", "test"):
            cache = by_question_split[(question_sha, split)]
            path = cache_root / cache["relative_path"]
            with np.load(path, allow_pickle=False) as values:
                assert set(values.files) == {"H", "y", "K", "layers"}
                assert values["H"].dtype == np.float16
                assert values["H"].shape == (cache["examples"], 1, 3)
                assert values["layers"].tolist() == [3]
                assert int(values["K"]) == cache["classes"]
                extracted, labels, classes, digest = _feature_cache(
                    cache_root, cache["relative_path"], 3, 4, [0],
                )
                assert extracted.shape == (cache["examples"], 3)
                assert len(labels) == cache["examples"]
                assert classes == cache["classes"]
                assert digest == cache["sha256"]

    repeated = dispatch({
        "schema_version": "threadforge.python.v1",
        "operation": "decision.distill.extract_gold",
        "params": params,
    })["result"]
    assert [item["sha256"] for item in repeated["caches"]] == [item["sha256"] for item in result["caches"]]


def test_distillation_extract_gold_rejects_overlapping_roots_and_mismatched_checkpoint(monkeypatch, tmp_path):
    params, _data_root, cache_root, model_path = _gold_feature_fixture(tmp_path)
    _stub_distillation_model(monkeypatch)
    overlap = json.loads(json.dumps(params))
    overlap["cache_root"] = overlap["data_root"]
    with pytest.raises(ProtocolError, match="roots must not overlap"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.extract_gold", "params": overlap})

    mismatch = json.loads(json.dumps(params))
    mismatch["model_checkpoint_sha256"] = "b" * 64
    with pytest.raises(ProtocolError, match="does not match"):
        dispatch({"schema_version": "threadforge.python.v1", "operation": "decision.distill.extract_gold", "params": mismatch})
    assert not list(cache_root.rglob("*.npz"))


def test_distillation_pipeline_composes_local_stages_and_binds_one_dataset_snapshot(monkeypatch, tmp_path):
    dataset_path, staging_root, cache_root = (tmp_path / "dataset", tmp_path / "staging", tmp_path / "cache")
    model_roots = {name: tmp_path / name for name in ("generator", "teacher", "student")}
    for path in (dataset_path, staging_root, cache_root, *model_roots.values()):
        path.mkdir()
    question = {"kind": "choice", "text": "Choose an action", "options": ["review", "close"]}
    question_id = "typed.customer_service.action"
    workflow = "customer_service"
    workflow_sha = hashlib.sha256(workflow.encode()).hexdigest()
    question_sha = hashlib.sha256(question_id.encode()).hexdigest()
    dataset_sha = "d" * 64
    generator_model, teacher_model, student_model = "fixture/generator", "fixture/teacher", "fixture/student"
    generator_checkpoint, teacher_checkpoint, student_checkpoint = "a" * 64, "b" * 64, "c" * 64
    student_sha = hashlib.sha256(student_model.encode()).hexdigest()
    calls = []

    def write_json(relative_path, document):
        path = staging_root / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        raw = json.dumps(document, sort_keys=True, separators=(",", ":")).encode()
        path.write_bytes(raw)
        return hashlib.sha256(raw).hexdigest()

    def export_cases(request):
        split = request["split"]
        calls.append(f"export-{split}")
        if split == "train":
            exemplar_path = f"distillation/source/{dataset_sha}/train/exemplars/{workflow_sha}.json"
            exemplar_sha = write_json(exemplar_path, {
                "schema_version": "threadforge.anyjev-distillation-exemplars.v1",
                "workflow": workflow,
                "examples": [{"case": f"exemplar-{index}"} for index in range(3)],
            })
            train_path = f"distillation/source/{dataset_sha}/train/train-cases/{workflow_sha}/{question_sha}.json"
            train_sha = write_json(train_path, {
                "schema_version": "threadforge.anyjev-distillation-train-cases.v1",
                "question_id": question_id, "question": question,
                "examples": [
                    {"state": {"case": f"pipeline-private-train-{index}"}, "gold_index": index % 2}
                    for index in range(8)
                ],
            })
            outputs = [
                {"role": "exemplars", "workflow_sha256": workflow_sha, "examples": 3,
                 "relative_path": exemplar_path, "sha256": exemplar_sha},
                {"role": "train_cases", "workflow_sha256": workflow_sha, "question_sha256": question_sha,
                 "examples": 8, "relative_path": train_path, "sha256": train_sha},
            ]
            example_count = 11
        else:
            test_path = f"distillation/source/{dataset_sha}/test/test-cases/{workflow_sha}/{question_sha}.json"
            test_sha = write_json(test_path, {
                "schema_version": "threadforge.anyjev-distillation-test-cases.v1",
                "question_id": question_id, "question": question,
                "examples": [
                    {"state": {"case": f"pipeline-private-test-{index}"}, "gold_index": index % 2}
                    for index in range(2)
                ],
            })
            outputs = [{
                "role": "test_cases", "workflow_sha256": workflow_sha, "question_sha256": question_sha,
                "examples": 2, "relative_path": test_path, "sha256": test_sha,
            }]
            example_count = 2
        return {
            "schema_version": "threadforge.anyjev-distillation-export-cases-result.v1",
            "dataset_sha256": dataset_sha, "split": split, "workflow_count": 1,
            "case_count": 1, "example_count": example_count, "output_count": len(outputs),
            "outputs": outputs, "human_review_required": True, "model_inference_verified": False,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    def synth(request):
        calls.append("synth")
        relative = f"distillation/synth/{generator_checkpoint}/{workflow_sha}.json"
        source_path = staging_root / request["workflows"][0]["exemplars_path"]
        source_sha = hashlib.sha256(source_path.read_bytes()).hexdigest()
        output_sha = write_json(relative, {
            "schema_version": "threadforge.anyjev-distillation-synthetic-states.v1",
            "workflow": workflow, "generator": generator_model, "n": 3,
            "generated": 3, "rejected": 0, "reject_reasons": {}, "settings": {},
            "states": [{"case": f"pipeline-private-synth-{index}"} for index in range(3)],
        })
        return {
            "schema_version": "threadforge.anyjev-distillation-synth-result.v1",
            "generator_sha256": hashlib.sha256(generator_model.encode()).hexdigest(),
            "generator_checkpoint_sha256": generator_checkpoint, "workflow_count": 1,
            "total_synthetic_examples": 3, "total_generated": 3, "total_rejected": 0,
            "outputs": [{"workflow_sha256": workflow_sha, "source_sha256": source_sha,
                         "examples": 3, "generated": 3, "rejected": 0,
                         "relative_path": relative, "sha256": output_sha}],
            "human_review_required": True, "model_inference_verified": True,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    def label(request):
        calls.append("teacher-label")
        outputs = []
        for role, split, examples in (
            ("teacher_synthetic_probabilities", "synth", 3),
            ("teacher_test_probabilities", "test", 2),
        ):
            outputs.append({
                "role": role, "question_sha256": question_sha,
                "teacher_sha256": hashlib.sha256(teacher_model.encode()).hexdigest(),
                "teacher_checkpoint_sha256": teacher_checkpoint, "source_sha256": "e" * 64,
                "examples": examples, "classes": 2, "block": 3,
                "relative_path": f"distillation/synth_labels/{teacher_checkpoint}/{question_sha}.{split}.npz",
                "sha256": "f" * 64 if split == "synth" else "1" * 64,
            })
        return {
            "schema_version": "threadforge.anyjev-distillation-label-result.v1",
            "teacher_sha256": hashlib.sha256(teacher_model.encode()).hexdigest(),
            "teacher_checkpoint_sha256": teacher_checkpoint, "question_count": 1,
            "total_synthetic_examples": 3, "total_test_examples": 2,
            "caches": outputs, "human_review_required": True, "model_inference_verified": True,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    def gold_features(request):
        model, checkpoint, role, block = (
            request["model"], request["model_checkpoint_sha256"], request["model_role"], request["block"],
        )
        calls.append(f"gold-{role}")
        model_sha = hashlib.sha256(model.encode()).hexdigest()
        receipts = []
        for split, examples in (("train", 8), ("test", 2)):
            receipts.append({
                "role": f"{role}_gold_{split}_features", "model_role": role,
                "question_sha256": question_sha, "model_sha256": model_sha,
                "model_checkpoint_sha256": checkpoint, "source_sha256": "2" * 64,
                "examples": examples, "classes": 2, "block": block, "feature_width": 4,
                "split": split,
                "relative_path": f"distillation/gold_features/{checkpoint}/{question_sha}.{split}.npz",
                "sha256": "3" * 64 if split == "train" else "4" * 64,
            })
        return {
            "schema_version": "threadforge.anyjev-distillation-extract-gold-result.v1",
            "model_role": role, "model_sha256": model_sha, "model_checkpoint_sha256": checkpoint,
            "question_count": 1, "total_train_examples": 8, "total_test_examples": 2,
            "cache_count": 2, "caches": receipts, "human_review_required": True,
            "model_inference_verified": True, "execution_verified": False,
            "cutover_evidence_eligible": False,
        }

    def synthetic_features(request):
        calls.append("student-synthetic-features")
        return {
            "schema_version": "threadforge.anyjev-distillation-extract-result.v1",
            "student_sha256": student_sha, "student_checkpoint_sha256": student_checkpoint,
            "question_count": 1, "total_synthetic_examples": 3,
            "caches": [{
                "role": "student_synthetic_features", "question_sha256": question_sha,
                "student_sha256": student_sha, "student_checkpoint_sha256": student_checkpoint,
                "source_sha256": "5" * 64, "examples": 3, "classes": 2, "block": 3,
                "feature_width": 4,
                "relative_path": f"distillation/synth_features/{student_checkpoint}/{question_sha}.synth.npz",
                "sha256": "6" * 64,
            }],
            "human_review_required": True, "model_inference_verified": True,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    def fit(request):
        calls.append("fit")
        assert request["questions"][0]["teacher_test_path"].endswith(f"{question_sha}.test.npz")
        assert request["questions"][0]["students"][0]["synth_features_path"].endswith(f"{question_sha}.synth.npz")
        summary = {"accuracy": 0.5, "expected_calibration_error": 0.1, "negative_log_loss": 0.69,
                   "examples": 2, "accuracy_ci95": [0.2, 0.8]}
        per_question_methods = {"teacher": {"accuracy": 0.5, "examples": 2}}
        return {
            "schema_version": "threadforge.anyjev-distillation-fit-result.v1",
            "teacher_sha256": hashlib.sha256(teacher_model.encode()).hexdigest(),
            "calibration_examples": 8, "budgets": [1, 2], "bootstrap_resamples": 100,
            "question_count": 1, "total_test_examples": 2, "teacher": summary,
            "students": [{"student_sha256": student_sha, "block": 3, "methods": {"teacher": summary}}],
            "per_question": [{"question_sha256": question_sha, "examples": 2, "students": [{
                "model_sha256": student_sha, "block": 3, "methods": per_question_methods, "budgets_used": [1, 2],
            }]}],
            "input_digests": [
                {"role": role, "question_sha256": question_sha, "sha256": str(index) * 64}
                for index, role in enumerate(("teacher_synthetic_probabilities", "teacher_test_probabilities",
                                              "student_gold_train_features", "student_gold_test_features",
                                              "student_synthetic_features"), 1)
            ],
            "human_review_required": True, "model_inference_verified": False,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    def soft(request):
        calls.append("soft")
        assert request["questions"][0]["teacher_train_path"].endswith(f"{question_sha}.train.npz")
        summary = {"accuracy": 0.5, "expected_calibration_error": 0.1, "negative_log_loss": 0.69,
                   "examples": 2, "accuracy_ci95": [0.2, 0.8]}
        per_question_methods = {"teacher": {"accuracy": 0.5, "examples": 2}}
        return {
            "schema_version": "threadforge.anyjev-distillation-soft-result.v1",
            "teacher_sha256": hashlib.sha256(teacher_model.encode()).hexdigest(), "teacher_block": 3,
            "question_count": 1, "total_test_examples": 2, "bootstrap_resamples": 100,
            "teacher": summary,
            "students": [{"student_sha256": student_sha, "block": 3, "methods": {"teacher": summary}}],
            "per_question": [{"question_sha256": question_sha, "examples": 2,
                               "teacher_accuracy": 0.5, "teacher_oof_agreement_with_gold": 0.5,
                               "students": [{"model_sha256": student_sha, "block": 3,
                                             "methods": per_question_methods}]}],
            "input_digests": [
                {"role": role, "question_sha256": question_sha, "sha256": str(index) * 64}
                for index, role in enumerate(("teacher_train_features", "teacher_test_features",
                                              "student_gold_train_features", "student_gold_test_features"), 1)
            ],
            "human_review_required": True, "model_inference_verified": False,
            "execution_verified": False, "cutover_evidence_eligible": False,
        }

    monkeypatch.setattr(distillation_module, "run_distillation_export_cases", export_cases)
    monkeypatch.setattr(distillation_module, "run_distillation_synth", synth)
    monkeypatch.setattr(distillation_module, "run_distillation_label", label)
    monkeypatch.setattr(distillation_module, "run_distillation_extract", synthetic_features)
    monkeypatch.setattr(distillation_module, "run_distillation_extract_gold", gold_features)
    monkeypatch.setattr(distillation_module, "run_distillation_fit", fit)
    monkeypatch.setattr(distillation_module, "run_distillation_soft", soft)

    request_params = {
        "dataset_path": str(dataset_path), "staging_root": str(staging_root), "cache_root": str(cache_root),
        "workflows": [workflow], "calibration_examples": 8, "budgets": [1, 2],
        "bootstrap_resamples": 100, "seed": 17,
        "generator": {
            "model": generator_model, "checkpoint_sha256": generator_checkpoint,
            "backend": {"type": "hf", "model_path": str(model_roots["generator"])},
            "per_workflow": 3,
        },
        "teacher": {
            "model": teacher_model, "checkpoint_sha256": teacher_checkpoint,
            "backend": {"type": "hf", "model_path": str(model_roots["teacher"])},
            "artifact": {"model": teacher_model, "artifacts": {}, "heads": {}},
            "block": 3, "n_layers": 4,
        },
        "students": [{
            "model": student_model, "checkpoint_sha256": student_checkpoint,
            "backend": {"type": "hf", "model_path": str(model_roots["student"])},
            "block": 3, "n_layers": 4,
        }],
    }
    schemas_dir = Path(__file__).resolve().parents[2] / "schema"
    request_schema = json.loads((schemas_dir / "anyjev-distillation-pipeline-request-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((schemas_dir / "anyjev-distillation-pipeline-result-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, result_schema):
        validator_for(schema).check_schema(schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-pipeline-request.v1", **request_params,
    })
    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.pipeline",
        "params": request_params,
    })["result"]
    schema_documents = [json.loads(path.read_text(encoding="utf-8")) for path in schemas_dir.glob("*.json")]
    from referencing import Registry, Resource
    registry = Registry().with_resources((doc["$id"], Resource.from_contents(doc)) for doc in schema_documents)
    validator_for(result_schema)(result_schema, registry=registry).validate(result)
    assert calls == ["export-train", "export-test", "synth", "teacher-label", "gold-teacher",
                     "student-synthetic-features", "gold-student", "fit", "soft"]
    assert result["dataset_sha256"] == dataset_sha
    assert result["question_count"] == 1 and result["student_count"] == 1
    assert result["stages"]["fit"]["question_count"] == result["stages"]["soft"]["question_count"] == 1
    assert result["model_inference_verified"] is True
    assert result["execution_verified"] is False and result["cutover_evidence_eligible"] is False
    rendered = json.dumps(result)
    for private in (str(dataset_path), str(staging_root), str(cache_root), str(model_roots["teacher"]),
                    question_id, "pipeline-private-"):
        assert private not in rendered


def _synth_fixture(tmp_path: Path, monkeypatch, generated_batches: list[list[str]]) -> tuple[dict, Path, Path]:
    source_root = tmp_path / "synth-source"
    cache_root = tmp_path / "synth-cache"
    model_path = tmp_path / "generator-model"
    source_root.mkdir()
    cache_root.mkdir()
    model_path.mkdir()
    (model_path / "config.json").write_text('{"model":"fixture"}', encoding="utf-8")
    examples = [
        {"customer": {"segment": "small", "balance": 125}, "approved": False},
        {"customer": {"segment": "medium", "balance": 450}, "approved": True},
        {"customer": {"segment": "large", "balance": 875}, "approved": False},
    ]
    (source_root / "exemplars.json").write_text(json.dumps({
        "schema_version": "threadforge.anyjev-distillation-exemplars.v1",
        "workflow": "fixture",
        "examples": examples,
    }), encoding="utf-8")
    checkpoint_sha = protocol_module._fingerprint_model_directory(str(model_path))["sha256"]
    tokenizer = SimpleNamespace(apply_chat_template=lambda messages, **_kwargs: messages[0]["content"])
    backend = SimpleNamespace(
        name="fixture/generator",
        _threadforge_model_environment_verified=True,
        _threadforge_model_checkpoint_sha256=checkpoint_sha,
        tokenizer=tokenizer,
    )
    monkeypatch.setattr(protocol_module, "_backend", lambda *_args, **_kwargs: backend)
    cursor = [0]

    def generate(prompts, *_args):
        batch = generated_batches[cursor[0]]
        cursor[0] += 1
        assert len(batch) == len(prompts)
        return batch

    monkeypatch.setattr("threadforge_py.distillation._generate_synthetic_batch", generate)
    params = {
        "source_root": str(source_root),
        "cache_root": str(cache_root),
        "generator_model": "fixture/generator",
        "generator_checkpoint_sha256": checkpoint_sha,
        "backend": {"type": "hf", "model_path": str(model_path), "device": "cpu", "dtype": "float32"},
        "per_workflow": 2,
        "batch_size": 2,
        "max_new_tokens": 64,
        "temperature": 1.0,
        "top_p": 0.95,
        "seed": 0,
        "max_attempts": 5,
        "workflows": [{"workflow": "fixture", "exemplars_path": "exemplars.json"}],
    }
    return params, cache_root, source_root


def test_distillation_synth_generates_review_only_schema_shaped_workflow_files(monkeypatch, tmp_path):
    first = {"customer": {"segment": "new", "balance": 520}, "approved": True}
    duplicate = {"customer": {"segment": "small", "balance": 125}, "approved": False}
    sensitive = {"customer": {"segment": "api_key=sk-12345678901234567890", "balance": 80}, "approved": False}
    second = {"customer": {"segment": "borderline", "balance": 300}, "approved": False}
    batches = [[json.dumps(first), json.dumps(duplicate)], [json.dumps(sensitive)], [json.dumps(second)]]
    params, cache_root, source_root = _synth_fixture(tmp_path, monkeypatch, batches)
    schema_dir = Path(__file__).resolve().parents[2] / "schema"
    request_schema = json.loads((schema_dir / "anyjev-distillation-synth-request-v1.schema.json").read_text(encoding="utf-8"))
    exemplars_schema = json.loads((schema_dir / "anyjev-distillation-exemplars-v1.schema.json").read_text(encoding="utf-8"))
    states_schema = json.loads((schema_dir / "anyjev-distillation-synthetic-states-v1.schema.json").read_text(encoding="utf-8"))
    result_schema = json.loads((schema_dir / "anyjev-distillation-synth-result-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, exemplars_schema, states_schema, result_schema):
        validator_for(schema).check_schema(schema)
    validator_for(request_schema)(request_schema).validate({
        "schema_version": "threadforge.anyjev-distillation-synth-request.v1", **params,
    })
    exemplars = json.loads((source_root / "exemplars.json").read_text(encoding="utf-8"))
    validator_for(exemplars_schema)(exemplars_schema).validate(exemplars)

    result = dispatch({
        "schema_version": "threadforge.python.v1", "operation": "decision.distill.synth", "params": params,
    })["result"]
    validator_for(result_schema)(result_schema).validate(result)
    assert result["workflow_count"] == 1
    assert result["total_synthetic_examples"] == 2
    assert result["total_generated"] == 4
    assert result["total_rejected"] == 2
    assert result["model_inference_verified"] is True
    assert result["human_review_required"] is True
    assert result["execution_verified"] is False
    assert result["cutover_evidence_eligible"] is False
    assert not any(token in json.dumps(result) for token in (
        "fixture/generator", str(cache_root), str(source_root), "api_key=", "borderline", "small",
    ))

    output = result["outputs"][0]
    output_path = cache_root / output["relative_path"]
    document = json.loads(output_path.read_text(encoding="utf-8"))
    validator_for(states_schema)(states_schema).validate(document)
    assert document["n"] == 2
    assert document["generated"] == 4
    assert document["rejected"] == 2
    assert document["reject_reasons"] == {"duplicate": 1, "sensitive": 1}
    assert document["states"] == [first, second]
    assert hashlib.sha256(output_path.read_bytes()).hexdigest() == output["sha256"]


def test_distillation_synth_fails_closed_on_invalid_or_exhausted_generation(monkeypatch, tmp_path):
    params, cache_root, _source_root = _synth_fixture(
        tmp_path,
        monkeypatch,
        [[json.dumps({"customer": {"segment": "wrong-shape"}, "approved": True})]],
    )
    params["per_workflow"] = 1
    params["batch_size"] = 1
    params["max_attempts"] = 1
    with pytest.raises(ProtocolError, match="did not reach the requested count"):
        dispatch({
            "schema_version": "threadforge.python.v1", "operation": "decision.distill.synth", "params": params,
        })
    assert not list(cache_root.rglob("*.json"))


def _typed_dataset_row(case_id: str, case_value: int) -> dict:
    questions = {
        "action": {
            "type": "choice", "instructions": "Choose the next step",
            "criteria": {"review": "Review the case", "close": "Close the case"},
        },
        "is_valid": {
            "type": "noul", "instructions": "The record is valid",
            "criteria": {"true": "The record meets policy", "false": "It violates policy"},
        },
        "severity": {
            "type": "score", "instructions": "Rate severity",
            "criteria": {"low": "Low impact", "high": "High impact"},
        },
    }
    gold = {
        "action": {"label": "close", "probabilities": {"review": 0.1, "close": 0.9}},
        "is_valid": {"label": "true", "probabilities": {"true": 0.8, "false": 0.2}},
        "severity": {"label": "1", "probabilities": {"0": 0.2, "1": 0.8}, "score": 1.0},
    }
    return {
        "id": case_id,
        "workflow": "customer_service",
        "state": json.dumps({"record": {"value": case_value}, "active": case_value % 2 == 0}),
        "questions": json.dumps(questions),
        "gold": json.dumps(gold),
    }


def test_distillation_export_cases_emits_local_train_and_test_contracts(monkeypatch, tmp_path):
    dataset_path = tmp_path / "dataset"
    output_root = tmp_path / "exported"
    dataset_path.mkdir()
    output_root.mkdir()
    (dataset_path / "dataset_info.json").write_text('{"local":"fixture"}', encoding="utf-8")
    split_rows = {
        "train": [_typed_dataset_row(f"train-{index}", index) for index in range(3)],
        "test": [_typed_dataset_row(f"test-{index}", index + 10) for index in range(2)],
    }
    monkeypatch.setattr(
        "threadforge_py.distillation._load_local_anyjev_split",
        lambda _path, split: split_rows[split],
    )
    request_schema_path = (Path(__file__).resolve().parents[2] / "schema" /
                           "anyjev-distillation-export-cases-request-v1.schema.json")
    result_schema_path = (Path(__file__).resolve().parents[2] / "schema" /
                          "anyjev-distillation-export-cases-result-v1.schema.json")
    request_schema = json.loads(request_schema_path.read_text(encoding="utf-8"))
    result_schema = json.loads(result_schema_path.read_text(encoding="utf-8"))
    exemplar_schema = json.loads((request_schema_path.parent /
                                  "anyjev-distillation-exemplars-v1.schema.json").read_text(encoding="utf-8"))
    train_cases_schema = json.loads((request_schema_path.parent /
                                     "anyjev-distillation-train-cases-v1.schema.json").read_text(encoding="utf-8"))
    cases_schema = json.loads((request_schema_path.parent /
                               "anyjev-distillation-test-cases-v1.schema.json").read_text(encoding="utf-8"))
    for schema in (request_schema, result_schema, exemplar_schema, train_cases_schema, cases_schema):
        validator_for(schema).check_schema(schema)

    params = {
        "dataset_path": str(dataset_path),
        "output_root": str(output_root),
        "workflows": ["customer_service"],
    }
    train_request = {
        "schema_version": "threadforge.anyjev-distillation-export-cases-request.v1",
        **params, "split": "train",
    }
    validator_for(request_schema)(request_schema).validate(train_request)
    train_result = dispatch({
        "schema_version": "threadforge.python.v1",
        "operation": "decision.distill.export_cases",
        "params": {key: value for key, value in train_request.items() if key != "schema_version"},
    })["result"]
    validator_for(result_schema)(result_schema).validate(train_result)
    assert train_result["case_count"] == 3
    assert train_result["output_count"] == 4
    assert train_result["example_count"] == 12
    exemplar_receipt = next(item for item in train_result["outputs"] if item["role"] == "exemplars")
    assert exemplar_receipt["role"] == "exemplars"
    exemplar_path = output_root / exemplar_receipt["relative_path"]
    exemplar_doc = json.loads(exemplar_path.read_text(encoding="utf-8"))
    validator_for(exemplar_schema)(exemplar_schema).validate(exemplar_doc)
    assert [item["record"]["value"] for item in exemplar_doc["examples"]] == [0, 1, 2]
    train_case_receipts = [item for item in train_result["outputs"] if item["role"] == "train_cases"]
    assert len(train_case_receipts) == 3
    for receipt in train_case_receipts:
        document = json.loads((output_root / receipt["relative_path"]).read_text(encoding="utf-8"))
        validator_for(train_cases_schema)(train_cases_schema).validate(document)
        assert len(document["examples"]) == 3
        expected_label = 0 if document["question_id"].endswith(".is_valid") else 1
        assert document["examples"][0]["gold_index"] == expected_label

    test_request = {
        "schema_version": "threadforge.anyjev-distillation-export-cases-request.v1",
        **params, "split": "test",
    }
    validator_for(request_schema)(request_schema).validate(test_request)
    test_result = dispatch({
        "schema_version": "threadforge.python.v1",
        "operation": "decision.distill.export_cases",
        "params": {key: value for key, value in test_request.items() if key != "schema_version"},
    })["result"]
    validator_for(result_schema)(result_schema).validate(test_result)
    assert test_result["case_count"] == 2
    assert test_result["output_count"] == 3
    assert test_result["example_count"] == 6
    question_docs = {}
    for receipt in test_result["outputs"]:
        assert receipt["role"] == "test_cases"
        path = output_root / receipt["relative_path"]
        document = json.loads(path.read_text(encoding="utf-8"))
        validator_for(cases_schema)(cases_schema).validate(document)
        assert len(document["examples"]) == 2
        question_docs[document["question_id"]] = document
    assert question_docs["typed.customer_service.action"]["examples"][0]["gold_index"] == 1
    assert question_docs["typed.customer_service.is_valid"]["question"]["kind"] == "noul"
    assert question_docs["typed.customer_service.is_valid"]["examples"][0]["gold_index"] == 0
    assert question_docs["typed.customer_service.severity"]["question"]["levels"] == ["Low impact", "High impact"]
    assert question_docs["typed.customer_service.severity"]["examples"][0]["gold_index"] == 1
    assert str(dataset_path) not in json.dumps(test_result)
    assert "test-0" not in json.dumps(question_docs)

    again = dispatch({
        "schema_version": "threadforge.python.v1",
        "operation": "decision.distill.export_cases",
        "params": {key: value for key, value in test_request.items() if key != "schema_version"},
    })["result"]
    assert [item["sha256"] for item in again["outputs"]] == [item["sha256"] for item in test_result["outputs"]]


def test_distillation_export_cases_rejects_overlap_mislabeled_split_and_changed_source(monkeypatch, tmp_path):
    dataset_path = tmp_path / "dataset"
    dataset_path.mkdir()
    marker = dataset_path / "dataset_info.json"
    marker.write_text('{"local":"fixture"}', encoding="utf-8")
    params = {
        "dataset_path": str(dataset_path),
        "output_root": str(dataset_path),
        "split": "train",
        "workflows": ["customer_service"],
    }
    with pytest.raises(ProtocolError, match="must not overlap"):
        dispatch({
            "schema_version": "threadforge.python.v1", "operation": "decision.distill.export_cases",
            "params": params,
        })

    output_root = tmp_path / "out"
    output_root.mkdir()
    params["output_root"] = str(output_root)
    params["split"] = "test"
    params["max_cases_per_workflow"] = 5001
    with pytest.raises(ProtocolError, match="5000"):
        dispatch({
            "schema_version": "threadforge.python.v1", "operation": "decision.distill.export_cases",
            "params": params,
        })

    params.pop("max_cases_per_workflow")
    params["split"] = "train"

    def mutate_while_reading(_path, _split):
        marker.write_text('{"local":"changed"}', encoding="utf-8")
        return [_typed_dataset_row(f"train-{index}", index) for index in range(3)]

    monkeypatch.setattr("threadforge_py.distillation._load_local_anyjev_split", mutate_while_reading)
    with pytest.raises(ProtocolError, match="changed during export"):
        dispatch({
            "schema_version": "threadforge.python.v1", "operation": "decision.distill.export_cases",
            "params": params,
        })
    assert not list(output_root.rglob("*.json"))
