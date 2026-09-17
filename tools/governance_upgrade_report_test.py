"""验证后续失败、空样本与资源清理失败不能产生通过报告。"""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("升级验收", Path(__file__).with_name("governance-upgrade-check.py"))
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class 升级报告测试(unittest.TestCase):
    def test_后续异常覆盖早先通过(self):
        for message in ("新增阶段失败", ""):
            evidence = {"结果": "通过", "错误": message}
            checker.complete_evidence(evidence, [("前面阶段", True)])
            self.assertEqual(evidence["结果"], "失败")

    def test_后续断言必须计入(self):
        checks = [("原有检查", True)]
        evidence = {}
        checker.complete_evidence(evidence, checks)
        checks.append(("新增检查", False))
        checker.complete_evidence(evidence, checks)
        self.assertEqual(evidence["结果"], "失败")
        self.assertEqual(len(evidence["断言"]), 2)

    def test_清理失败与空样本不能通过(self):
        for evidence, checks in [({}, []), ({"资源清理": [{"结果": "失败"}]}, [("业务检查", True)])]:
            checker.complete_evidence(evidence, checks)
            self.assertEqual(evidence["结果"], "失败")

    def test_完整样本通过(self):
        evidence = {"资源清理": [{"结果": "完成"}]}
        checker.complete_evidence(evidence, [("业务检查", True)])
        self.assertEqual(evidence["结果"], "通过")


if __name__ == "__main__":
    unittest.main()
