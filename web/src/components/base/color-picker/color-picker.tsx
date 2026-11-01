"use client";

import {
  ColorArea as AriaColorArea,
  ColorField as AriaColorField,
  ColorPicker as AriaColorPicker,
  ColorSlider as AriaColorSlider,
  ColorSwatch as AriaColorSwatch,
  ColorSwatchPicker as AriaColorSwatchPicker,
  ColorSwatchPickerItem as AriaColorSwatchPickerItem,
  ColorThumb as AriaColorThumb,
  Input as AriaInput,
  SliderTrack as AriaSliderTrack,
} from "react-aria-components";
import type { ColorPickerProps as AriaColorPickerProps } from "react-aria-components";
import { Popover, PopoverTrigger } from "@/components/base/popover/popover";
import type { PopoverProps } from "@/components/base/popover/popover";
import { cx } from "@/utils/cx";

/**
 * Color picker: choose a colour by eye or by hex: a brand colour, a label,
 * a chart series.
 *
 *   <ColorPicker value={color} onChange={setColor} />
 *   <ColorWell aria-label="Brand color" value={color} onChange={setColor} />
 *
 * Promoted from the theme customizer's picker, with its look: a
 * saturation and brightness field and a hue rail, both with the slider's
 * white embossed thumb, and a hex row on the input's tertiary surface.
 * `showAlpha` adds an opacity rail over a checkerboard, and `swatches` adds
 * a row of presets. `ColorWell` is a round swatch that opens the same
 * editor in a popover.
 *
 * Built on React Aria's color components, so the colour is kept in HSB
 * while you drag (its hue survives a trip through grey), each control is
 * reachable by keyboard (arrows nudge, Shift or Page keys take bigger
 * steps), and the hex field takes any CSS colour and commits on Enter or
 * blur. Values are hex strings or React Aria `Color` objects.
 */

/** The slider's white embossed thumb, filled with the colour under it. */
const THUMB = [
  "size-4 rounded-full border border-white shadow-[0_2px_4px_rgb(0_0_0/0.18),inset_0_0_0_1px_rgb(0_0_0/0.06)]",
  "transition-[scale] duration-150 ease-out data-[dragging]:scale-110",
  "data-[focus-visible]:ring-2 data-[focus-visible]:ring-border-focus-ring data-[focus-visible]:ring-offset-2",
].join(" ");

/** The inset hairline that keeps light colours from melting into the page. */
const HAIRLINE = "pointer-events-none absolute inset-0 shadow-[inset_0_0_0_1px_rgb(0_0_0/0.06)]";

const CHECKERBOARD =
  "bg-[repeating-conic-gradient(#e6e6e6_0%_25%,#ffffff_0%_50%)] bg-[length:8px_8px] dark:bg-[repeating-conic-gradient(#3a3a3a_0%_25%,#262626_0%_50%)]";

interface EditorProps {
  showAlpha?: boolean;
  swatches?: string[];
  className?: string;
}

/** The controls, bound to the nearest React Aria ColorPicker. */
function Editor({ showAlpha = false, swatches, className }: EditorProps) {
  return (
    <div className={cx("flex w-[228px] max-w-full flex-col gap-2.5", className)}>
      <AriaColorArea
        colorSpace="hsb"
        xChannel="saturation"
        yChannel="brightness"
        className="relative aspect-[3/2] w-full shrink-0 cursor-crosshair rounded-2xl"
      >
        <span aria-hidden className={cx(HAIRLINE, "rounded-2xl")} />
        <AriaColorThumb className={THUMB} />
      </AriaColorArea>

      <AriaColorSlider colorSpace="hsb" channel="hue" className="w-full">
        <AriaSliderTrack className="relative h-3 w-full cursor-pointer rounded-full">
          <span aria-hidden className={cx(HAIRLINE, "rounded-full")} />
          <AriaColorThumb className={cx(THUMB, "top-1/2")} />
        </AriaSliderTrack>
      </AriaColorSlider>

      {showAlpha ? (
        <AriaColorSlider channel="alpha" className="w-full">
          <div className={cx("relative h-3 w-full rounded-full", CHECKERBOARD)}>
            <AriaSliderTrack className="relative h-3 w-full cursor-pointer rounded-full">
              <span aria-hidden className={cx(HAIRLINE, "rounded-full")} />
              <AriaColorThumb className={cx(THUMB, "top-1/2")} />
            </AriaSliderTrack>
          </div>
        </AriaColorSlider>
      ) : null}

      <div className="flex items-center gap-2">
        <AriaColorSwatch className="relative size-8 shrink-0 rounded-full shadow-[inset_0_0_0_1px_rgb(0_0_0/0.06)]" />
        <AriaColorField aria-label="Hex color" className="min-w-0 flex-1">
          <AriaInput
            dir="ltr"
            spellCheck={false}
            onKeyDown={(event) => {
              // React Aria commits the field on blur only; Enter commits it
              // too, and focus stays in the field.
              if (event.key !== "Enter") return;
              const input = event.currentTarget;
              input.blur();
              input.focus();
            }}
            className={cx(
              "h-8 w-full min-w-0 rounded-2lg bg-background-tertiary-default px-2.5 font-mono text-[13px] text-text-primary uppercase",
              "outline-none ring-2 ring-transparent ring-inset transition-[background-color,box-shadow] duration-150 ease",
              "focus:bg-background-primary-default focus:ring-border-focus-ring",
            )}
          />
        </AriaColorField>
      </div>

      {swatches?.length ? (
        <AriaColorSwatchPicker aria-label="Presets" className="grid grid-cols-7 gap-1.5 pt-0.5">
          {swatches.map((swatch) => (
            <AriaColorSwatchPickerItem
              key={swatch}
              color={swatch}
              className={cx(
                "group/swatch relative aspect-square w-full cursor-pointer rounded-full outline-none transition-[scale] duration-150 ease-out hover:scale-110",
                "data-[focus-visible]:ring-2 data-[focus-visible]:ring-border-focus-ring data-[focus-visible]:ring-offset-2",
              )}
            >
              <AriaColorSwatch className="size-full rounded-full shadow-[inset_0_0_0_1px_rgb(0_0_0/0.08)]" />
              {/* Selected: a white ring inside a dark hairline reads on any colour. */}
              <span
                aria-hidden
                className="pointer-events-none absolute inset-0 rounded-full opacity-0 shadow-[inset_0_0_0_2px_#fff,inset_0_0_0_3px_rgb(0_0_0/0.25)] transition-opacity duration-150 group-data-[selected]/swatch:opacity-100"
              />
            </AriaColorSwatchPickerItem>
          ))}
        </AriaColorSwatchPicker>
      ) : null}
    </div>
  );
}

export interface ColorPickerProps extends Omit<AriaColorPickerProps, "children"> {
  /** Add an opacity rail. Default false. */
  showAlpha?: boolean;
  /** Preset colours, as CSS colour strings, in a row under the editor. */
  swatches?: string[];
  /** Classes on the editor, e.g. a width. Default 228px. */
  className?: string;
}

export function ColorPicker({ showAlpha, swatches, className, ...props }: ColorPickerProps) {
  return (
    <AriaColorPicker {...props}>
      <Editor showAlpha={showAlpha} swatches={swatches} className={className} />
    </AriaColorPicker>
  );
}

export interface ColorWellProps extends ColorPickerProps {
  /** Names the swatch button and its popover. Default "Color". */
  "aria-label"?: string;
  /** Where the editor opens. Default "bottom start". */
  placement?: PopoverProps["placement"];
  /** Classes on the swatch button, e.g. a size. Default 32px. */
  wellClassName?: string;
}

/** A round swatch that opens the editor in a popover. */
export function ColorWell({
  "aria-label": ariaLabel = "Color",
  placement = "bottom start",
  showAlpha,
  swatches,
  className,
  wellClassName,
  ...props
}: ColorWellProps) {
  const label = ariaLabel;

  return (
    <AriaColorPicker {...props}>
      <PopoverTrigger>
        <button
          type="button"
          aria-label={label}
          className={cx(
            "relative size-8 shrink-0 cursor-pointer rounded-full outline-none transition-[scale] duration-150 ease-out hover:scale-110",
            "focus-visible:ring-2 focus-visible:ring-border-focus-ring focus-visible:ring-offset-2",
            wellClassName,
          )}
        >
          <AriaColorSwatch className="size-full rounded-full shadow-[inset_0_0_0_1px_rgb(0_0_0/0.08)]" />
        </button>
        <Popover aria-label={label} placement={placement} className="w-[248px] rounded-3xl" dialogClassName="p-1">
          <Editor showAlpha={showAlpha} swatches={swatches} className={cx("w-full", className)} />
        </Popover>
      </PopoverTrigger>
    </AriaColorPicker>
  );
}
