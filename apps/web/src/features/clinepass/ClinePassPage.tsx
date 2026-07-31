import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import {
  IconExternalLink,
  IconPencil,
  IconPlus,
  IconRefreshCw,
  IconShield,
  IconTimer,
  IconTrash2,
} from '@/components/ui/icons';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useInterval } from '@/hooks/useInterval';
import {
  clinePassApi,
  type ClinePassAccount,
  type ClinePassUsageLimit,
  type ClinePassUsageLimits,
} from '@/services/api';
import { useNotificationStore } from '@/stores';
import type { ApiError } from '@/types';
import styles from './ClinePassPage.module.scss';

type AccountUsageState = {
  data: ClinePassUsageLimits | null;
  loading: boolean;
  errorCode: string;
  errorMessage: string;
};

const emptyUsageState = (): AccountUsageState => ({
  data: null,
  loading: false,
  errorCode: '',
  errorMessage: '',
});

const supportedLimitTypes = ['five_hour', 'weekly', 'monthly'];

const readApiErrorCode = (error: unknown): string => {
  const details = (error as ApiError | undefined)?.details;
  if (!details || typeof details !== 'object') return '';
  const code = (details as Record<string, unknown>).code;
  return typeof code === 'string' ? code : '';
};

export function ClinePassPage() {
  const { t, i18n } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const [accounts, setAccounts] = useState<ClinePassAccount[]>([]);
  const [usageByAccount, setUsageByAccount] = useState<Record<string, AccountUsageState>>({});
  const [loading, setLoading] = useState(true);
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [pageError, setPageError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<ClinePassAccount | null>(null);
  const [label, setLabel] = useState('');
  const [sessionId, setSessionId] = useState('');
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [, setClock] = useState(() => Date.now());

  useInterval(() => setClock(Date.now()), 60_000);

  const refreshUsage = useCallback(async (accountId: string) => {
    setUsageByAccount((current) => ({
      ...current,
      [accountId]: {
        ...(current[accountId] ?? emptyUsageState()),
        loading: true,
        errorCode: '',
        errorMessage: '',
      },
    }));
    try {
      const data = await clinePassApi.getUsageLimits(accountId);
      setUsageByAccount((current) => ({
        ...current,
        [accountId]: { data, loading: false, errorCode: '', errorMessage: '' },
      }));
    } catch (error: unknown) {
      const errorMessage = error instanceof Error ? error.message : t('common.unknown_error');
      setUsageByAccount((current) => ({
        ...current,
        [accountId]: {
          data: current[accountId]?.data ?? null,
          loading: false,
          errorCode: readApiErrorCode(error),
          errorMessage,
        },
      }));
    }
  }, [t]);

  const loadAccounts = useCallback(async () => {
    setLoading(true);
    setPageError('');
    try {
      const response = await clinePassApi.list();
      const nextAccounts = response.items ?? [];
      setAccounts(nextAccounts);
      setUsageByAccount((current) =>
        Object.fromEntries(
          nextAccounts.map((account) => [account.id, current[account.id] ?? emptyUsageState()])
        )
      );
      await Promise.all(nextAccounts.map((account) => refreshUsage(account.id)));
    } catch (error: unknown) {
      setPageError(error instanceof Error ? error.message : t('common.unknown_error'));
    } finally {
      setLoading(false);
    }
  }, [refreshUsage, t]);

  useEffect(() => {
    void loadAccounts();
  }, [loadAccounts]);

  useHeaderRefresh(loadAccounts);

  const handleRefreshAll = useCallback(async () => {
    setRefreshingAll(true);
    await Promise.all(accounts.map((account) => refreshUsage(account.id)));
    setRefreshingAll(false);
  }, [accounts, refreshUsage]);

  const openCreateModal = () => {
    setEditingAccount(null);
    setLabel('');
    setSessionId('');
    setFormError('');
    setModalOpen(true);
  };

  const openEditModal = (account: ClinePassAccount) => {
    setEditingAccount(account);
    setLabel(account.label);
    setSessionId('');
    setFormError('');
    setModalOpen(true);
  };

  const closeModal = () => {
    if (saving) return;
    setModalOpen(false);
    setEditingAccount(null);
    setSessionId('');
    setFormError('');
  };

  const handleSave = async () => {
    const normalizedLabel = label.trim();
    const normalizedSessionId = sessionId.trim();
    if (!normalizedLabel) {
      setFormError(t('clinepass.label_required'));
      return;
    }
    if (!editingAccount && !normalizedSessionId) {
      setFormError(t('clinepass.session_required'));
      return;
    }

    setSaving(true);
    setFormError('');
    try {
      const account = editingAccount
        ? await clinePassApi.update(editingAccount.id, {
            label: normalizedLabel,
            ...(normalizedSessionId ? { sessionId: normalizedSessionId } : {}),
          })
        : await clinePassApi.create({
            label: normalizedLabel,
            sessionId: normalizedSessionId,
          });
      setAccounts((current) => {
        const exists = current.some((item) => item.id === account.id);
        return exists
          ? current.map((item) => (item.id === account.id ? account : item))
          : [...current, account];
      });
      setModalOpen(false);
      setEditingAccount(null);
      setSessionId('');
      showNotification(
        t(editingAccount ? 'clinepass.account_updated' : 'clinepass.account_added'),
        'success'
      );
      await refreshUsage(account.id);
    } catch (error: unknown) {
      setFormError(error instanceof Error ? error.message : t('common.unknown_error'));
    } finally {
      setSaving(false);
    }
  };

  const confirmDelete = (account: ClinePassAccount) => {
    showConfirmation({
      title: t('clinepass.delete_title'),
      message: t('clinepass.delete_message', { name: account.label }),
      confirmText: t('common.delete'),
      variant: 'danger',
      onConfirm: async () => {
        try {
          await clinePassApi.delete(account.id);
          setAccounts((current) => current.filter((item) => item.id !== account.id));
          setUsageByAccount((current) => {
            const next = { ...current };
            delete next[account.id];
            return next;
          });
          showNotification(t('clinepass.account_deleted'), 'success');
        } catch (error: unknown) {
          showNotification(
            error instanceof Error ? error.message : t('common.unknown_error'),
            'error'
          );
          throw error;
        }
      },
    });
  };

  const accountCountLabel = useMemo(
    () => t('clinepass.account_count', { count: accounts.length }),
    [accounts.length, t]
  );

  const formatAbsoluteTime = (value: string | number) => {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return t('clinepass.time_unknown');
    return new Intl.DateTimeFormat(i18n.language, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }).format(date);
  };

  const formatReset = (value?: string) => {
    if (!value) return t('clinepass.reset_unknown');
    const resetAt = new Date(value).getTime();
    if (Number.isNaN(resetAt)) return t('clinepass.reset_unknown');
    const minutes = Math.ceil((resetAt - Date.now()) / 60_000);
    if (minutes <= 0) return t('clinepass.reset_due');
    if (minutes < 60) return t('clinepass.reset_in_minutes', { count: minutes });
    const hours = Math.ceil(minutes / 60);
    if (hours < 48) return t('clinepass.reset_in_hours', { count: hours });
    return t('clinepass.reset_in_days', { count: Math.ceil(hours / 24) });
  };

  const getUsageErrorText = (state: AccountUsageState) => {
    if (state.errorCode) {
      const key = `clinepass.error_${state.errorCode}`;
      const translated = t(key, { defaultValue: '' });
      if (translated) return translated;
    }
    return state.errorMessage || t('clinepass.load_failed');
  };

  const renderLimit = (limit: ClinePassUsageLimit) => {
    const used = Math.max(0, Math.min(100, Number(limit.percentUsed) || 0));
    const remaining = Math.max(0, 100 - used);
    const status = used >= 100 ? 'reached' : used >= 75 ? 'warning' : 'healthy';
    const labelKey = `clinepass.limit_${limit.type}`;
    const label = t(labelKey, { defaultValue: limit.type });
    return (
      <div key={limit.type} className={styles.limit} data-status={status}>
        <div className={styles.limitHeader}>
          <span className={styles.limitName}>{label}</span>
          <span className={styles.remainingValue}>
            {t('clinepass.remaining_percent', { value: Math.round(remaining) })}
          </span>
        </div>
        <div className={styles.progressTrack} aria-label={label} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(used)}>
          <span className={styles.progressValue} style={{ width: `${used}%` }} />
        </div>
        <div className={styles.limitMeta}>
          <span>{t('clinepass.used_percent', { value: Math.round(used) })}</span>
          <span title={limit.resetsAt ? formatAbsoluteTime(limit.resetsAt) : undefined}>
            <IconTimer size={13} />
            {formatReset(limit.resetsAt)}
          </span>
        </div>
      </div>
    );
  };

  return (
    <div className={styles.page}>
      <section className={styles.intro}>
        <div>
          <p className={styles.kicker}>{t('clinepass.kicker')}</p>
          <h1>{t('clinepass.title')}</h1>
          <p className={styles.description}>{t('clinepass.description')}</p>
        </div>
        <div className={styles.introActions}>
          <span className={styles.accountCount}>{accountCountLabel}</span>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleRefreshAll}
            loading={refreshingAll}
            disabled={accounts.length === 0 || loading}
          >
            <IconRefreshCw size={15} />
            {t('clinepass.refresh_all')}
          </Button>
          <Button size="sm" onClick={openCreateModal}>
            <IconPlus size={15} />
            {t('clinepass.add_account')}
          </Button>
        </div>
      </section>

      <div className={styles.securityNote}>
        <IconShield size={17} />
        <span>{t('clinepass.security_note')}</span>
      </div>

      {pageError && (
        <div className={styles.pageError} role="alert">
          <span>{pageError}</span>
          <Button variant="secondary" size="xs" onClick={() => void loadAccounts()}>
            {t('common.retry')}
          </Button>
        </div>
      )}

      {loading && accounts.length === 0 ? (
        <div className={styles.accountGrid} aria-busy="true">
          {[0, 1].map((item) => (
            <div className={styles.skeletonCard} key={item}>
              <span className={styles.skeletonTitle} />
              <span className={styles.skeletonLine} />
              <span className={styles.skeletonBlock} />
            </div>
          ))}
        </div>
      ) : accounts.length === 0 && !pageError ? (
        <section className={styles.emptyPanel}>
          <EmptyState
            title={t('clinepass.empty_title')}
            description={t('clinepass.empty_description')}
            action={
              <Button size="sm" onClick={openCreateModal}>
                <IconPlus size={15} />
                {t('clinepass.add_account')}
              </Button>
            }
          />
        </section>
      ) : (
        <div className={styles.accountGrid}>
          {accounts.map((account) => {
            const state = usageByAccount[account.id] ?? emptyUsageState();
            const limits = (state.data?.limits ?? [])
              .filter((limit) => supportedLimitTypes.includes(limit.type))
              .sort(
                (left, right) =>
                  supportedLimitTypes.indexOf(left.type) - supportedLimitTypes.indexOf(right.type)
              );
            return (
              <article className={styles.accountCard} key={account.id}>
                <header className={styles.accountHeader}>
                  <div>
                    <div className={styles.accountHeadingRow}>
                      <span className={styles.statusDot} data-error={Boolean(state.errorMessage)} />
                      <h2>{account.label}</h2>
                    </div>
                    <p>
                      {state.data
                        ? t('clinepass.updated_at', {
                            time: formatAbsoluteTime(state.data.fetchedAtMs),
                          })
                        : t('clinepass.not_updated')}
                    </p>
                  </div>
                  <div className={styles.accountActions}>
                    <Button
                      variant="ghost"
                      size="xs"
                      iconOnly
                      loading={state.loading}
                      onClick={() => void refreshUsage(account.id)}
                      aria-label={t('clinepass.refresh_account', { name: account.label })}
                      title={t('clinepass.refresh_account', { name: account.label })}
                    >
                      <IconRefreshCw size={15} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="xs"
                      iconOnly
                      onClick={() => openEditModal(account)}
                      aria-label={t('clinepass.edit_account', { name: account.label })}
                      title={t('clinepass.edit_account', { name: account.label })}
                    >
                      <IconPencil size={15} />
                    </Button>
                    <Button
                      variant="ghost"
                      size="xs"
                      iconOnly
                      onClick={() => confirmDelete(account)}
                      aria-label={t('clinepass.delete_account', { name: account.label })}
                      title={t('clinepass.delete_account', { name: account.label })}
                    >
                      <IconTrash2 size={15} />
                    </Button>
                  </div>
                </header>

                {state.errorMessage ? (
                  <div className={styles.usageError} role="status">
                    <div>
                      <strong>{t('clinepass.load_failed')}</strong>
                      <span>{getUsageErrorText(state)}</span>
                    </div>
                    <Button variant="secondary" size="xs" onClick={() => void refreshUsage(account.id)} loading={state.loading}>
                      {t('common.retry')}
                    </Button>
                  </div>
                ) : state.loading && !state.data ? (
                  <div className={styles.limitSkeletons} aria-busy="true">
                    {[0, 1, 2].map((item) => <span key={item} />)}
                  </div>
                ) : limits.length > 0 ? (
                  <div className={styles.limits}>{limits.map(renderLimit)}</div>
                ) : (
                  <div className={styles.noLimits}>{t('clinepass.no_limits')}</div>
                )}
              </article>
            );
          })}
        </div>
      )}

      <a
        className={styles.dashboardLink}
        href="https://app.cline.bot/dashboard/subscription?personal=true"
        target="_blank"
        rel="noreferrer"
      >
        {t('clinepass.open_dashboard')}
        <IconExternalLink size={14} />
      </a>

      <Modal
        open={modalOpen}
        onClose={closeModal}
        closeDisabled={saving}
        title={t(editingAccount ? 'clinepass.edit_modal_title' : 'clinepass.add_modal_title')}
        width={560}
        footer={
          <div className={styles.modalFooter}>
            <Button variant="ghost" onClick={closeModal} disabled={saving}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void handleSave()} loading={saving}>
              {t('common.save')}
            </Button>
          </div>
        }
      >
        <div className={styles.form}>
          <Input
            label={t('clinepass.label_field')}
            value={label}
            maxLength={100}
            onChange={(event) => setLabel(event.target.value)}
            placeholder={t('clinepass.label_placeholder')}
            autoFocus
          />
          <Input
            label={t('clinepass.session_field')}
            value={sessionId}
            type="password"
            onChange={(event) => setSessionId(event.target.value)}
            placeholder={
              editingAccount
                ? t('clinepass.session_placeholder_edit')
                : t('clinepass.session_placeholder')
            }
            hint={
              editingAccount
                ? t('clinepass.session_hint_edit')
                : t('clinepass.session_hint')
            }
            autoComplete="new-password"
            spellCheck={false}
          />
          {formError && <div className={styles.formError}>{formError}</div>}
        </div>
      </Modal>
    </div>
  );
}
