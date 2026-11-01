import json
from pathlib import Path

import pytest
from jsonschema.exceptions import ValidationError
from jsonschema.validators import validator_for


SCHEMA_PATH = Path(__file__).resolve().parents[2] / "schema" / "rrsi-evaluation-summary-v1.schema.json"


def test_rrsi_evaluation_summary_schema_accepts_complete_partial_unknown_and_legacy_costs():
    schema = json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))
    validator = validator_for(schema)(schema)

    def result(domain, task_metrics, usd_cost=None):
        payload = {
            "schema_version": "threadforge.rrsi-evaluation.v1",
            "domain": domain,
            "job": "base",
            "trials_per_task": 2,
            "score": 0.5,
            "mean_tokens": 150,
            "expected_trials": 2,
            "missing_trials": 0,
            "metrics": {"pass_rate": 0.5},
            "per_task": {
                "task-hash-alias": {
                    "rewards": [1.0, 0.0],
                    "weights": [1.0, 1.0],
                    "tokens": [100, 200],
                    "missing": 0,
                    "metrics": task_metrics,
                }
            },
        }
        if usd_cost is not None:
            payload["usd_cost"] = usd_cost
        return payload

    validator.validate(result("coding", {"trial_cost_usd": [0.125, 0.25]}, {
        "source_reported": True, "total_usd": 0.375, "records": 2,
        "expected_records": 2, "complete": True,
        "source": "harbor_agent_result_cost_usd_v1",
    }))
    validator.validate(result("eng", {"trial_cost_usd": [0.125, None]}, {
        "source_reported": True, "total_usd": None, "records": 1,
        "expected_records": 2, "complete": False,
        "source": "openrouter_account_usage_cost_v1",
    }))
    validator.validate(result("workspace", {"trial_cost_usd": [None, None]}, {
        "source_reported": True, "total_usd": None, "records": 0,
        "expected_records": 2, "complete": False, "source": None,
    }))
    validator.validate(result("coding", {}))

    invalid = [
        result("coding", {"trial_cost_usd": [0.125, None]}, {
            "source_reported": True, "total_usd": 0.125, "records": 1,
            "expected_records": 2, "complete": False,
        }),
        result("coding", {"trial_cost_usd": [True, 0.25]}, {
            "source_reported": True, "total_usd": 0.375, "records": 2,
            "expected_records": 2, "complete": True,
        }),
        result("coding", {"trial_cost_usd": [-0.1, 0.25]}, {
            "source_reported": True, "total_usd": 0.15, "records": 2,
            "expected_records": 2, "complete": True,
        }),
        result("coding", {}, {
            "source_reported": False, "total_usd": 0, "records": 0,
            "expected_records": 2, "complete": False,
        }),
        result("eng", {"trial_cost_usd": [0.125, 0.25]}, {
            "source_reported": True, "total_usd": 0.375, "records": 2,
            "expected_records": 2, "complete": True,
            "source": "model-price-estimate",
        }),
    ]
    for payload in invalid:
        with pytest.raises(ValidationError):
            validator.validate(payload)
