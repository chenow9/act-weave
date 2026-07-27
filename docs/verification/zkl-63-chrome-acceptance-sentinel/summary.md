# ZKL-63 Chrome Dual-Profile Acceptance

- Base: http://127.0.0.1:5174
- Target user (disposable): zkl63.b.ms2skl8u
- PASS: 18  FAIL: 0  SKIP: 0

| Case | Status | Detail |
|---|---|---|
| 1_normal_login_smoke | PASS | url=/overview |
| 2_high02_client_safe_error | PASS | status=401 code=UNAUTHENTICATED |
| setup_create_target_user | PASS | username=zkl63.b.ms2skl8u |
| 4_temp_login_forced_change_password | PASS | /change-password |
| 4b_overview_redirect_to_change_password | PASS | /change-password |
| 5_server_must_change_flag | PASS | mustChangePassword=true |
| 5_server_gate_business_403 | PASS | status=403 code=PASSWORD_CHANGE_REQUIRED |
| 5_server_gate_me_allowed | PASS | status=200 |
| 6_complete_change_password | PASS | url=/login?passwordChanged=1 changePosts=1 |
| 6_old_token_401 | PASS | status=401 |
| 6_new_password_login | PASS | /overview |
| 3_reset_invalidates_access_token | PASS | reset=204 me=401 |
| setup_rechange_after_reset | PASS | status=204 |
| 7_demote_invalidates_access_token | PASS | demote=200 adminList=401 |
| 7_relogin_user_admin_forbidden | PASS | role=USER status=403 code=FORBIDDEN |
| 8_lock_invalidates_access_token | PASS | lock=200 me=401 |
| 8_disable_invalidates_access_token | PASS | disable=200 me=401 |
| 9_main_path_admin | PASS | users=true workspaces=true |

Evidence screenshots are PNG files in this directory (no secrets).
