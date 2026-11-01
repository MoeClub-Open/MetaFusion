"""Keep independent services' API aggregation code and DDL aligned (no hashing)."""
from pathlib import Path

root = Path(__file__).resolve().parent.parent
source = root / "backend/internal/telemetry"
for name in ("auth", "community", "storage"):
    target = root.parent / f"metafusion-{name}"
    if not target.is_dir():
        print(f"skip: {target.name} not checked out")
        continue
    for file in ("telemetry.go", "schema.sql"):
        assert (target / "internal/telemetry" / file).read_text(encoding="utf-8") == (source / file).read_text(encoding="utf-8"), f"{target.name}: telemetry/{file} differs"
print("API aggregation sources are aligned")
