import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Badge, Tabs } from "./ui";

describe("ui primitives", () => {
  it("renders badge label", () => {
    render(<Badge kind="ok">可关联</Badge>);
    expect(screen.getByText("可关联")).toHaveClass("badge", "ok");
  });

  it("switches tabs", () => {
    const onChange = vi.fn();
    render(
      <Tabs
        value="user"
        onChange={onChange}
        items={[
          { value: "user", label: "用户会话" },
          { value: "scheduled", label: "定时任务" },
        ]}
      />,
    );
    fireEvent.click(screen.getByText("定时任务"));
    expect(onChange).toHaveBeenCalledWith("scheduled");
  });
});
