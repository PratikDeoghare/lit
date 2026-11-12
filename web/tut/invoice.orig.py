import sys

TAX = 0.0825


def price_for(qty, unit):
    # volume pricing
    if qty >= 100:
        unit = unit * 0.85
    elif qty >= 10:
        unit = unit * 0.95
    return unit


def invoice(items, region="TX"):
    sub = 0
    lines = []
    for name, qty, unit in items:
        p = price_for(qty, unit)
        amt = round(p * qty, 2)
        lines.append((name, qty, p, amt))
        sub += amt
    tax = 0 if region == "OR" else round(sub * TAX, 2)
    total = round(sub + tax, 2)
    return lines, sub, tax, total


def render(lines, sub, tax, total):
    out = []
    for name, qty, p, amt in lines:
        out.append("%-12s %4d x %8.2f = %10.2f" % (name, qty, p, amt))
    out.append("%-12s %29.2f" % ("subtotal", sub))
    out.append("%-12s %29.2f" % ("tax", tax))
    out.append("%-12s %29.2f" % ("TOTAL", total))
    return "\n".join(out)


if __name__ == "__main__":
    items = [("widget", 12, 3.50), ("gizmo", 1, 19.99), ("bolt", 250, 0.12)]
    print(render(*invoice(items, sys.argv[1] if len(sys.argv) > 1 else "TX")))
