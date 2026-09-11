// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Button } from "./Button";
import { Stepper } from "./Stepper";
import { ProgressBar } from "./ProgressBar";

afterEach(cleanup);

describe("playlist controls", () => {
  it("defaults buttons to non-submit and forwards disabled and click behavior", () => {
    const onClick = vi.fn();
    const { rerender } = render(<Button onClick={onClick}>Generate</Button>);
    expect(screen.getByRole("button").getAttribute("type")).toBe("button");
    fireEvent.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledOnce();
    rerender(<Button disabled type="submit" variant="primary" size="sm" onClick={onClick}>Generate</Button>);
    fireEvent.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledOnce();
    expect(screen.getByRole("button").getAttribute("type")).toBe("submit");
  });

  it("clamps count changes and disables controls at the boundaries", () => {
    const onChange = vi.fn();
    const { rerender } = render(<Stepper value={5} min={3} max={7} step={4} label="tracks" hint="total" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "increase tracks" }));
    expect(onChange).toHaveBeenLastCalledWith(7);
    fireEvent.click(screen.getByRole("button", { name: "decrease tracks" }));
    expect(onChange).toHaveBeenLastCalledWith(3);
    rerender(<Stepper value={1} onChange={onChange} />);
    expect((screen.getByRole("button", { name: "decrease value" }) as HTMLButtonElement).disabled).toBe(true);
    rerender(<Stepper value={99} onChange={onChange} />);
    expect((screen.getByRole("button", { name: "increase value" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it.each([[-1, "0"], [5, "50"], [11, "100"]])("clamps progress %i and exposes the percentage", (done, percent) => {
    render(<ProgressBar done={done} total={10} label="Downloading" />);
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe(percent);
    expect(screen.getByText(`${done} / 10`)).toBeTruthy();
  });

  it("keeps indeterminate progress distinct from zero and uses explicit status", () => {
    const { rerender } = render(<ProgressBar />);
    expect(screen.getByRole("progressbar").hasAttribute("aria-valuenow")).toBe(false);
    rerender(<ProgressBar total={10} note="Verifying" size={8} />);
    expect(screen.getByRole("progressbar").getAttribute("aria-valuetext")).toBe("Verifying");
    expect(screen.getByRole("progressbar").style.height).toBe("8px");
  });
});
