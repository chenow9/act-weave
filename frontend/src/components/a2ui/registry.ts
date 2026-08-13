import type { Component } from "vue";

import type { A2UIComponentName } from "./generated/catalog.gen";
import A2UIButton from "./parts/A2UIButton.vue";
import A2UICard from "./parts/A2UICard.vue";
import A2UIChart from "./parts/A2UIChart.vue";
import A2UICheckBox from "./parts/A2UICheckBox.vue";
import A2UIChoicePicker from "./parts/A2UIChoicePicker.vue";
import A2UIColumn from "./parts/A2UIColumn.vue";
import A2UIDateTimeInput from "./parts/A2UIDateTimeInput.vue";
import A2UIDivider from "./parts/A2UIDivider.vue";
import A2UIRow from "./parts/A2UIRow.vue";
import A2UIText from "./parts/A2UIText.vue";
import A2UITextField from "./parts/A2UITextField.vue";

/**
 * Dispatch table keyed by exact component name: no aliases, no case folding.
 *
 * It is a total record on purpose. Forgetting a catalog component fails
 * type-check here rather than degrading at runtime to a placeholder for a
 * component we do in fact support.
 */
export const registry: Record<A2UIComponentName, Component> = {
  Column: A2UIColumn,
  Row: A2UIRow,
  Card: A2UICard,
  Text: A2UIText,
  Divider: A2UIDivider,
  Chart: A2UIChart,
  TextField: A2UITextField,
  CheckBox: A2UICheckBox,
  ChoicePicker: A2UIChoicePicker,
  DateTimeInput: A2UIDateTimeInput,
  Button: A2UIButton,
};
