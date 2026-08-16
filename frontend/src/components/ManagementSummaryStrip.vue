<script lang="ts">
export interface ManagementSummaryItem {
  label: string;
  value: string | number;
  icon: string;
  note?: string;
  ariaLabel?: string;
  tone?: "default" | "info" | "warning" | "danger";
}
</script>

<script setup lang="ts">
import { useI18n } from "vue-i18n";

withDefaults(defineProps<{ items: ManagementSummaryItem[]; compact?: boolean }>(), { compact: false });
const { t } = useI18n();
</script>

<template>
  <section class="management-summary-strip" :class="{ compact }" :aria-label="t('common.pageSummaryAria')">
    <article
      v-for="item in items"
      :key="item.label"
      :class="`tone-${item.tone || 'default'}`"
      :aria-label="item.ariaLabel"
    >
      <span :aria-hidden="item.ariaLabel ? 'true' : undefined"
        ><i :class="item.icon" aria-hidden="true" />{{ item.label }}</span
      >
      <strong :aria-hidden="item.ariaLabel ? 'true' : undefined"
        >{{ item.value }}<small v-if="item.note">{{ item.note }}</small></strong
      >
    </article>
  </section>
</template>

<style scoped>
.management-summary-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin: 0;
  overflow: hidden;
  border: 1px solid #f3f4f6;
  border-radius: 1.25rem;
  background: #fff;
  box-shadow: 0 4px 20px -4px rgba(0, 0, 0, 0.04);
}

.management-summary-strip article {
  min-width: 0;
  padding: 1.1rem 1.2rem;
  border-right: 1px solid #f3f4f6;
}

.management-summary-strip article:last-child {
  border-right: 0;
}

.management-summary-strip span {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  color: #6b7280;
  font-size: 0.85rem;
  font-weight: 600;
}

.management-summary-strip i {
  color: #10b981;
}

.management-summary-strip .tone-info i {
  color: #2563eb;
}

.management-summary-strip .tone-warning i {
  color: #d97706;
}

.management-summary-strip .tone-danger i {
  color: #e11d48;
}

.management-summary-strip strong {
  display: block;
  margin-top: 0.6rem;
  color: #111827;
  font-size: 1.75rem;
  font-weight: 700;
  letter-spacing: -0.025em;
}

.management-summary-strip small {
  margin-left: 0.35rem;
  color: #6b7280;
  font-size: 0.85rem;
  font-weight: 600;
}

.management-summary-strip.compact article {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  min-height: 52px;
  padding: 10px 16px;
}

.management-summary-strip.compact strong {
  margin-top: 0;
  font-size: 1.2rem;
}

.management-summary-strip.compact span {
  font-size: 0.78rem;
}

@media (max-width: 980px) {
  .management-summary-strip {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .management-summary-strip article:nth-child(2) {
    border-right: 0;
  }

  .management-summary-strip article:nth-child(-n + 2) {
    border-bottom: 1px solid #f3f4f6;
  }
}

@media (max-width: 700px) {
  .management-summary-strip {
    grid-template-columns: 1fr;
  }

  .management-summary-strip article,
  .management-summary-strip article:nth-child(2) {
    border-right: 0;
    border-bottom: 1px solid #f3f4f6;
  }

  .management-summary-strip article:last-child {
    border-bottom: 0;
  }
}
</style>
