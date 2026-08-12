/**
 * Dispatch table. Lookup is by exact component name: no lowercasing, no aliases,
 * no shape guessing. The type is a total record over the catalog, so a component
 * added to the catalog fails the build here until it has a renderer.
 */

import type { A2UIRegistry } from "./generated/catalog.gen";
import { renderChart } from "./chart";
import { renderButton, renderCheckBox, renderChoicePicker, renderDateTimeInput, renderTextField } from "./components/inputs";
import { renderCard, renderColumn, renderDivider, renderRow } from "./components/layout";
import { renderText } from "./components/text";

export const registry: A2UIRegistry<string> = {
  Column: renderColumn,
  Row: renderRow,
  Card: renderCard,
  Text: renderText,
  Divider: renderDivider,
  Chart: renderChart,
  TextField: renderTextField,
  CheckBox: renderCheckBox,
  ChoicePicker: renderChoicePicker,
  DateTimeInput: renderDateTimeInput,
  Button: renderButton,
};
