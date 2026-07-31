import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import {
  IconCode,
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
  thirdPartyQuotaApi,
  type ThirdPartyQuotaAccount,
  type ThirdPartyQuotaProvider,
  type ThirdPartyQuotaUsageLimit,
  type ThirdPartyQuotaUsageLimits,
} from '@/services/api';
import { useNotificationStore } from '@/stores';
import type { ApiError } from '@/types';
import styles from './ClinePassPage.module.scss';

type AccountUsageState = {
  data: ThirdPartyQuotaUsageLimits | null;
  loading: boolean;
  errorCode: string;
  errorMessage: string;
};

type ProviderDefinition = {
  id: ThirdPartyQuotaProvider;
  icon: ReactNode;
  dashboardURL: string;
};

const emptyUsageState = (): AccountUsageState => ({
  data: null,
  loading: false,
  errorCode: '',
  errorMessage: '',
});

const supportedLimitTypes = ['five_hour', 'weekly', 'monthly'];
const defaultOpenCodeServerFunctionID =
  'c7389bd0e731f80f49593e5ee53835475f4e28594dd6bd83eb229bab753498cd';

const providerDefinitions: ProviderDefinition[] = [
  {
    id: 'clinepass',
    icon: <span className={styles.clineMark}>C</span>,
    dashboardURL: 'https://app.cline.bot/dashboard/subscription?personal=true',
  },
  {
    id: 'opencode_go',
    icon: <IconCode size={17} />,
    dashboardURL: 'https://opencode.ai/workspace',
  },
];

const readApiErrorCode = (error: unknown): string => {
  const details = (error as ApiError | undefined)?.details;
  if (!details || typeof details !== 'object') return '';
  const code = (details as Record<string, unknown>).code;
  return typeof code === 'string' ? code : '';
};

export function ThirdPartyQuotaPage() {
  const { t, i18n } = useTranslation();
  const { showNotification, showConfirmation } = useNotificationStore();
  const [accounts, setAccounts] = useState<ThirdPartyQuotaAccount[]>([]);
  const [usageByAccount, setUsageByAccount] = useState<Record<string, AccountUsageState>>({});
  const [loading, setLoading] = useState(true);
  const [refreshingAll, setRefreshingAll] = useState(false);
  const [pageError, setPageError] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<ThirdPartyQuotaAccount | null>(null);
  const [provider, setProvider] = useState<ThirdPartyQuotaProvider>('clinepass');
  const [label, setLabel] = useState('');
  const [credential, setCredential] = useState('');
  const [workspaceId, setWorkspaceId] = useState('');
  const [serverFunctionId, setServerFunctionId] = useState('');
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState('');
  const [, setClock] = useState(() => Date.now());

  useInterval(() => setClock(Date.now()), 60_000);

  const refreshUsage = useCallback(
    async (accountId: string) => {
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
        const data = await thirdPartyQuotaApi.getUsageLimits(accountId);
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
    },
    [t]
  );

  const loadAccounts = useCallback(async () => {
    setLoading(true);
    setPageError('');
    try {
      const response = await thirdPartyQuotaApi.list();
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
    setProvider('clinepass');
    setLabel('');
    setCredential('');
    setWorkspaceId('');
    setServerFunctionId('');
    setFormError('');
    setModalOpen(true);
  };

  const openEditModal = (account: ThirdPartyQuotaAccount) => {
    setEditingAccount(account);
    setProvider(account.provider);
    setLabel(account.label);
    setCredential('');
    setWorkspaceId(account.workspaceId ?? '');
    setServerFunctionId(account.serverFunctionId ?? '');
    setFormError('');
    setModalOpen(true);
  };

  const closeModal = () => {
    if (saving) return;
    setModalOpen(false);
    setEditingAccount(null);
    setCredential('');
    setFormError('');
  };

  const handleSave = async () => {
    const normalizedLabel = label.trim();
    const normalizedCredential = credential.trim();
    const normalizedWorkspaceId = workspaceId.trim();
    const normalizedServerFunctionId = serverFunctionId.trim();
    if (!normalizedLabel) {
      setFormError(t('clinepass.label_required'));
      return;
    }
    if (!editingAccount && !normalizedCredential) {
      setFormError(t(`clinepass.credential_required_${provider}`));
      return;
    }
    if (provider === 'opencode_go' && !normalizedWorkspaceId) {
      setFormError(t('clinepass.workspace_required'));
      return;
    }

    setSaving(true);
    setFormError('');
    try {
      const account = editingAccount
        ? await thirdPartyQuotaApi.update(editingAccount.id, {
            label: normalizedLabel,
            ...(normalizedCredential ? { credential: normalizedCredential } : {}),
            ...(provider === 'opencode_go'
              ? {
                  workspaceId: normalizedWorkspaceId,
                  serverFunctionId:
                    normalizedServerFunctionId || defaultOpenCodeServerFunctionID,
                }
              : {}),
          })
        : await thirdPartyQuotaApi.create({
            provider,
            label: normalizedLabel,
            credential: normalizedCredential,
            ...(provider === 'opencode_go'
              ? {
                  workspaceId: normalizedWorkspaceId,
                  ...(normalizedServerFunctionId
                    ? { serverFunctionId: normalizedServerFunctionId }
                    : {}),
                }
              : {}),
          });
      setAccounts((current) => {
        const exists = current.some((item) => item.id === account.id);
        return exists
          ? current.map((item) => (item.id === account.id ? account : item))
          : [...current, account];
      });
      setModalOpen(false);
      setEditingAccount(null);
      setCredential('');
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

  const confirmDelete = (account: ThirdPartyQuotaAccount) => {
    showConfirmation({
      title: t('clinepass.delete_title'),
      message: t('clinepass.delete_message', {
        name: account.label,
        provider: t(`clinepass.provider_${account.provider}`),
      }),
      confirmText: t('common.delete'),
      variant: 'danger',
      onConfirm: async () => {
        try {
          await thirdPartyQuotaApi.delete(account.id);
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

  const renderLimit = (limit: ThirdPartyQuotaUsageLimit) => {
    const used = Math.max(0, Math.min(100, Number(limit.percentUsed) || 0));
    const remaining = Math.max(0, 100 - used);
    const status = used >= 100 ? 'reached' : used >= 75 ? 'warning' : 'healthy';
    const labelKey = `clinepass.limit_${limit.type}`;
    const limitLabel = t(labelKey, { defaultValue: limit.type });
    return (
      <div key={limit.type} className={styles.limit} data-status={status}>
        <div className={styles.limitHeader}>
          <span className={styles.limitName}>{limitLabel}</span>
          <span className={styles.remainingValue}>
            {t('clinepass.remaining_percent', { value: Math.round(remaining) })}
          </span>
        </div>
        <div
          className={styles.progressTrack}
          aria-label={limitLabel}
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(used)}
        >
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

  const renderAccount = (account: ThirdPartyQuotaAccount) => {
    const state = usageByAccount[account.id] ?? emptyUsageState();
    const limits = (state.data?.limits ?? [])
      .filter((limit) => supportedLimitTypes.includes(limit.type))
      .sort(
        (left, right) =>
          supportedLimitTypes.indexOf(left.type) - supportedLimitTypes.indexOf(right.type)
      );
    return (
      <article className={styles.accountCard} key={account.id} data-provider={account.provider}>
        <header className={styles.accountHeader}>
          <div className={styles.accountIdentity}>
            <div className={styles.accountHeadingRow}>
              <span className={styles.statusDot} data-error={Boolean(state.errorMessage)} />
              <h3>{account.label}</h3>
              <span className={styles.providerBadge} data-provider={account.provider}>
                {t(`clinepass.provider_${account.provider}`)}
              </span>
            </div>
            <p>
              {state.data
                ? t('clinepass.updated_at', { time: formatAbsoluteTime(state.data.fetchedAtMs) })
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

        {account.provider === 'opencode_go' && state.data && (
          <div className={styles.providerMeta}>
            <span data-active={state.data.useBalance === true}>
              {state.data.useBalance
                ? t('clinepass.balance_fallback_on')
                : t('clinepass.balance_fallback_off')}
            </span>
            {state.data.mine === false && <span>{t('clinepass.subscription_not_owner')}</span>}
            {state.data.regions && state.data.regions.length > 0 && (
              <span>{t('clinepass.regions', { value: state.data.regions.join(' · ') })}</span>
            )}
          </div>
        )}

        {state.errorMessage ? (
          <div className={styles.usageError} role="status">
            <div>
              <strong>{t('clinepass.load_failed')}</strong>
              <span>{getUsageErrorText(state)}</span>
            </div>
            <Button
              variant="secondary"
              size="xs"
              onClick={() => void refreshUsage(account.id)}
              loading={state.loading}
            >
              {t('common.retry')}
            </Button>
          </div>
        ) : state.loading && !state.data ? (
          <div className={styles.limitSkeletons} aria-busy="true">
            {[0, 1, 2].map((item) => (
              <span key={item} />
            ))}
          </div>
        ) : limits.length > 0 ? (
          <div className={styles.limits}>{limits.map(renderLimit)}</div>
        ) : (
          <div className={styles.noLimits}>{t('clinepass.no_limits')}</div>
        )}
      </article>
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
        <div className={styles.sourceList}>
          {providerDefinitions.map((definition) => {
            const providerAccounts = accounts.filter(
              (account) => account.provider === definition.id
            );
            if (providerAccounts.length === 0) return null;
            return (
              <section className={styles.sourceSection} key={definition.id}>
                <header className={styles.sourceHeader}>
                  <div className={styles.sourceIdentity} data-provider={definition.id}>
                    <span className={styles.sourceIcon}>{definition.icon}</span>
                    <div>
                      <div className={styles.sourceTitleRow}>
                        <h2>{t(`clinepass.provider_${definition.id}`)}</h2>
                        <span>{t('clinepass.source_badge')}</span>
                      </div>
                      <p>{t(`clinepass.provider_description_${definition.id}`)}</p>
                    </div>
                  </div>
                  <a
                    className={styles.dashboardLink}
                    href={definition.dashboardURL}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t('clinepass.open_provider_dashboard', {
                      provider: t(`clinepass.provider_${definition.id}`),
                    })}
                    <IconExternalLink size={14} />
                  </a>
                </header>
                <div className={styles.accountGrid}>{providerAccounts.map(renderAccount)}</div>
              </section>
            );
          })}
        </div>
      )}

      <Modal
        open={modalOpen}
        onClose={closeModal}
        closeDisabled={saving}
        title={t(editingAccount ? 'clinepass.edit_modal_title' : 'clinepass.add_modal_title')}
        width={600}
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
          <fieldset className={styles.providerPicker} disabled={Boolean(editingAccount)}>
            <legend>{t('clinepass.provider_field')}</legend>
            <div>
              {providerDefinitions.map((definition) => (
                <button
                  type="button"
                  key={definition.id}
                  data-active={provider === definition.id}
                  onClick={() => setProvider(definition.id)}
                >
                  <span className={styles.sourceIcon}>{definition.icon}</span>
                  <span>
                    <strong>{t(`clinepass.provider_${definition.id}`)}</strong>
                    <small>{t(`clinepass.provider_picker_${definition.id}`)}</small>
                  </span>
                </button>
              ))}
            </div>
          </fieldset>
          <Input
            label={t('clinepass.label_field')}
            value={label}
            maxLength={100}
            onChange={(event) => setLabel(event.target.value)}
            placeholder={t('clinepass.label_placeholder')}
            autoFocus
          />
          {provider === 'opencode_go' && (
            <Input
              label={t('clinepass.workspace_field')}
              value={workspaceId}
              onChange={(event) => setWorkspaceId(event.target.value)}
              placeholder="wrk_..."
              hint={t('clinepass.workspace_hint')}
              spellCheck={false}
            />
          )}
          <Input
            label={t(`clinepass.credential_field_${provider}`)}
            value={credential}
            type="password"
            onChange={(event) => setCredential(event.target.value)}
            placeholder={
              editingAccount
                ? t('clinepass.credential_placeholder_edit')
                : t(`clinepass.credential_placeholder_${provider}`)
            }
            hint={
              editingAccount
                ? t('clinepass.credential_hint_edit')
                : t(`clinepass.credential_hint_${provider}`)
            }
            autoComplete="new-password"
            spellCheck={false}
          />
          {provider === 'opencode_go' && (
            <Input
              label={t('clinepass.server_function_field')}
              value={serverFunctionId}
              onChange={(event) => setServerFunctionId(event.target.value)}
              placeholder={defaultOpenCodeServerFunctionID}
              hint={t('clinepass.server_function_hint')}
              spellCheck={false}
            />
          )}
          {formError && <div className={styles.formError}>{formError}</div>}
        </div>
      </Modal>
    </div>
  );
}
