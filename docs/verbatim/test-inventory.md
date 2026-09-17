# C# test inventory (source of the Go test port)

Generated 2026-09-16 from `tests/CodeFlow.Tests` at commit `2f86d34` (v2.7.1). One entry per xUnit test method; the
name is the behaviour it pins (sentences with underscores). Port each class to `backend/<feature>/<class>_test.go`
and tick it off. `[T]` marks a `[Theory]` (data-driven; usually fed by `docs/business-rules/test-vectors`),
other entries are `[Fact]`s or platform-conditional facts.

## Activity

### ActivityCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `Every_shape_is_spelled_the_way_the_renderer_reads_it`

### ActivityLogStoreTests

- `One_conversation_stays_one_activity_even_when_the_engine_changes_session_id`
- `Three_conversations_stay_three_even_when_the_engine_reports_one_sentinel`
- `A_turn_recorded_before_session_tracking_existed_is_invisible_forever`
- `Search_covers_the_whole_exchange_and_not_just_the_title`
- `A_renamed_conversation_keeps_its_turns_and_only_swaps_the_title`
- `Turns_come_back_oldest_first_so_the_transcript_reads_in_order`
- `Every_field_of_a_turn_survives_a_round_trip`
- `The_last_turn_provider_is_what_guards_a_cross_engine_resume`
- `Deleting_a_conversation_takes_its_title_with_it_and_leaves_the_others_alone`
- `Deleting_the_project_cascades_to_its_whole_transcript`

### JobHistoryStoreTests

- `Runs_come_back_newest_first`
- `A_run_reads_back_exactly_as_it_was_returned`
- `Renaming_writes_the_custom_label_and_leaves_the_generated_one_alone`
- `Deleting_a_run_that_is_still_in_flight_is_not_an_error`
- `Deleting_the_project_cascades_to_its_job_history`

## Ai

### AgentStreamingTests

- `Every_line_a_process_prints_is_streamed_as_it_happens`
- `The_payload_is_spelled_the_way_the_renderer_reads_it`
- `Stdout_and_stderr_are_tagged_separately`
- `A_run_can_be_cancelled_mid_stream`
- `Cancelling_an_unknown_run_reports_that_nothing_was_in_flight`
- `Cancelling_a_run_that_just_ended_answers_instead_of_throwing`
- `A_run_that_never_ends_is_cut_short_by_its_own_deadline`
- `A_run_that_keeps_printing_outlives_its_deadline_many_times_over`
- `A_stop_still_reads_as_a_stop_when_a_deadline_is_also_set`
- `A_very_long_line_is_capped_before_it_crosses_the_boundary`
- `A_long_line_of_emoji_is_not_cut_through_a_surrogate_pair`
- `Blank_lines_never_reach_the_log`
- `A_progress_bar_rewritten_with_carriage_returns_stays_one_line`
- `The_trace_keeps_the_tail_of_a_chatty_run`
- `An_untracked_run_is_captured_but_never_published`
- `Stdin_is_piped_in_and_closed_so_the_child_sees_eof`
- `The_real_cli_streams_events_and_ends_with_a_result`

### AiCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `A_chat_reply_is_spelled_the_way_the_renderer_reads_it`
- `A_stored_trace_is_spelled_the_way_the_renderer_rehydrates_it`
- [T] `Each_known_provider_resolves_to_its_own_engine`
- [T] `Local_is_an_alias_of_ollama`
- [T] `An_unrecognised_provider_falls_back_to_claude`
- `Only_claude_defines_a_dedicated_commit_model`
- `The_two_http_engines_are_the_non_agentic_ones`
- `An_http_engine_never_offers_a_subprocess_to_run`
- `The_openai_key_rides_on_the_transport_and_never_on_an_invocation`
- `Only_codex_folds_the_whole_brief_into_stdin`
- [T] `Codex_allows_a_workspace_without_git_and_preserves_its_sandbox`
- `Every_built_in_template_has_text`
- `The_prompts_extracted_from_rust_literals_carry_no_stray_indentation`

### AiIpcTests

- `A_chat_turn_streams_its_log_answers_from_the_result_event_and_is_recorded`
- `An_analysis_streams_under_its_job_id_and_lands_in_the_job_list`
- `Stopping_a_run_cancels_it_and_leaves_no_turn_behind`

### AiOperationsTests

- `A_commit_message_from_an_empty_diff_is_refused_before_anything_is_spawned`
- `A_blank_template_falls_back_to_the_built_in_and_a_stored_one_wins`
- `A_commit_message_carries_no_footer`
- `A_commit_message_wrapped_in_a_fence_arrives_unwrapped`
- [T] `The_built_in_commit_template_pairs_every_conventional_type_with_its_emoji`
- `An_analysis_with_nothing_uncommitted_is_refused_in_spanish_behind_a_marker`
- `An_analysis_payload_is_the_project_context_then_the_diff`
- `An_analysis_with_no_enabled_context_sends_only_the_scope_and_the_diff`
- [T] `The_scope_line_tells_the_model_which_diff_it_is_looking_at`
- [T] `Judging_only_uncommitted_work_against_a_ticket_carries_its_caveat`
- `A_working_tree_ticket_review_names_no_base_branch`
- `An_empty_branch_contribution_is_refused_with_its_own_sentence`
- `An_analysis_is_stamped_with_the_model_the_cli_reported_not_the_one_configured`
- `An_analysis_with_no_model_anywhere_says_so_in_spanish`
- `The_first_turn_establishes_the_context_and_the_next_one_does_not`
- `An_engine_that_cannot_resume_gets_the_context_on_every_turn`
- `The_chat_carries_the_message_on_the_prompt_and_never_in_the_payload`
- `The_chat_always_auto_approves_edits`
- `A_non_agentic_provider_refuses_to_apply_a_fix_rather_than_pretending_to`
- `A_fix_runs_on_the_engines_write_tools_and_not_the_users_allow_list`
- `An_inline_edit_with_nothing_selected_is_refused`
- `An_inline_edit_sends_the_file_the_selection_and_the_instruction`
- [T] `A_wrapping_code_fence_is_stripped_from_anything_written_to_a_buffer`
- `A_conflict_gets_all_three_sides_labelled_in_spanish`
- `A_failing_engine_surfaces_its_message_unchanged`

### AiRoutingTests

- `The_ten_task_keys_are_verbatim`
- `With_nothing_configured_every_task_routes_to_claude`
- `A_per_task_provider_wins_over_the_global_one`
- `A_blank_stored_value_counts_as_unset_at_every_step`
- `A_per_task_model_wins_over_the_providers_base_model`
- `The_commit_task_falls_to_the_engines_own_commit_model_before_the_base_model`
- `A_per_task_commit_model_still_wins_over_the_engines_own`
- `A_provider_with_no_commit_model_of_its_own_falls_straight_to_its_base_model`
- `An_unconfigured_model_is_the_empty_string`
- `The_binary_path_setting_wins_over_the_engines_default`
- `Allowed_tools_split_on_commas_and_drop_blanks`
- [T] `A_judging_task_with_no_setting_falls_back_to_the_recommended_three`
- [T] `A_task_that_is_not_judging_is_left_alone`
- `A_list_the_user_cleared_stays_cleared`
- `An_agent_driving_a_judging_task_is_bounded_like_everyone_else`
- `Resolving_for_an_agent_takes_its_model_verbatim_and_ignores_task_routing`
- `A_shared_template_prefers_the_current_key_and_falls_back_to_the_legacy_one`

### AiTextFooterTests

- `What_the_run_consumed_is_stamped_beside_what_produced_it`
- `An_engine_that_reported_nothing_stamps_nothing_extra`
- `Tokens_are_stamped_even_when_the_engine_priced_nothing`
- `The_stamp_that_was_there_before_is_still_there`
- `A_blank_model_still_reads_as_a_sentence`

### BinaryDiscoveryTests

- `The_install_dirs_come_before_the_inherited_path`
- `The_install_dirs_cover_the_places_the_cli_installers_use`
- `A_configured_path_is_used_exactly_as_stored`
- `A_binary_outside_the_process_path_still_resolves_to_a_full_path`
- `Launching_and_detecting_look_in_the_same_place`
- `An_unresolvable_name_is_passed_through_untouched`
- `A_dot_in_the_name_does_not_stop_the_search_off_windows`
- `On_windows_an_exe_beats_a_cmd_shim_in_the_same_directory`
- `On_windows_an_earlier_directory_beats_a_later_one_whatever_the_extension`
- `Finding_a_binary_that_is_not_there_returns_null`
- `Finding_a_binary_that_is_there_returns_its_path`

### ChatTurnTests

- `A_successful_turn_is_recorded_with_everything_the_reply_is_stamped_with`
- `A_failed_turn_is_recorded_so_the_error_outlives_the_next_message`
- `A_turn_the_user_stopped_is_not_history_at_all`
- `A_caller_that_names_no_conversation_still_gets_its_turn_recorded`
- `A_resume_token_survives_a_second_turn_on_the_same_provider`
- `A_resume_token_minted_by_another_provider_is_dropped`
- `A_conversation_with_no_recorded_provider_keeps_its_token`
- `An_enabled_context_reaches_the_turn_and_a_disabled_one_does_not`
- `An_active_agents_prompt_frames_the_turn_ahead_of_every_context`
- `An_unknown_project_is_refused_before_any_engine_is_reached`
- `A_finished_analysis_is_filed_under_the_job_id_the_ui_already_renders`
- `A_failed_analysis_is_filed_as_an_error_so_it_is_still_there_tomorrow`
- `A_cancelled_analysis_writes_no_job_row`
- `An_analysis_of_a_clean_working_tree_is_refused_and_writes_no_job_row`

### ClaudeCodeTests

- [T] `Interpret_matches_the_extracted_vector`
- `The_invocation_carries_the_flags_stream_json_requires`
- `A_tool_list_bounds_what_exists_and_not_only_what_is_approved`
- `No_tool_list_leaves_the_engines_own_defaults_alone`
- `An_empty_tool_list_is_an_answer_rather_than_the_absence_of_one`
- `A_run_never_loads_the_analysed_repositorys_own_settings`
- `A_finished_run_reports_what_it_consumed`
- `An_engine_that_reports_nothing_reports_null_rather_than_zero`
- `Usage_survives_a_result_that_carries_no_cost`
- `Quota_refusals_are_tagged_for_the_frontend`

### EngineScratchTests

- `Scratch_writers_place_prefixed_artifacts_under_the_temp_root`
- `Collect_recognises_only_scratch_arguments_and_delete_removes_them`
- `The_sweep_claims_old_orphans_and_leaves_young_and_foreign_entries_alone`
- `A_missing_temp_root_is_not_an_error`

### EngineVectorTests

- [T] `Interpret_matches_the_extracted_vector`
- [T] `The_rollout_id_is_scraped_out_of_the_preamble`
- [T] `The_model_cache_keeps_listed_entries_in_priority_order`
- `A_small_brief_stays_on_argv`
- `A_brief_composing_engine_is_handed_nothing_on_stdin`
- [T] `The_reason_is_pulled_out_of_an_error_body`
- [T] `Non_chat_model_families_are_filtered_out_of_the_picker`
- [T] `Ansi_escapes_are_stripped_before_an_engine_sees_the_output`
- [T] `A_version_is_read_out_of_whatever_banner_the_cli_printed`
- [T] `A_refusal_is_recognised_as_a_quota_signal`
- [T] `A_lost_login_is_recognised_as_an_auth_signal`

### NetworkRetryTests

- `A_review_whose_CLI_never_reached_the_network_is_simply_run_again`
- `A_run_that_may_have_written_files_is_never_repeated`
- `A_failure_that_is_not_the_network_still_fails_the_first_time`

### PromptsTests

- `The_two_review_standards_share_the_finding_format_verbatim`
- [T] `The_ticket_standard_carries_its_own_contract`
- [T] `The_re_sync_literals_are_in_both_standards`
- `The_verdict_headers_the_prompt_asks_for_are_the_ones_the_parser_looks_for`

### ReviewOperationTests

- [T] `The_level_directive_rides_at_the_end_of_the_prompt`
- `The_three_directives_say_different_things_about_confidence`
- `Ultra_only_sends_a_run_looking_for_code_when_there_is_code_to_look_at`
- `The_extracted_code_rides_after_the_diff`
- `The_payload_names_the_pull_request_before_the_diff`
- `A_blank_description_reads_as_no_description`
- `Enabled_contexts_ride_under_their_own_heading`
- `A_pull_request_with_nothing_in_it_is_refused_before_the_engine_runs`
- `A_workspace_template_replaces_the_built_in_methodology`
- `A_blank_template_falls_back_to_the_built_in_one`
- `The_run_comes_back_whole_for_the_pipeline_to_stamp`

### StdinDeliveryTests

- `A_child_that_never_reads_its_input_leaves_the_run_intact`
- `A_child_that_drains_its_input_reports_a_complete_delivery`
- `An_engine_fed_on_stdin_refuses_an_answer_formed_from_part_of_it`
- `An_engine_that_does_not_use_stdin_is_unbothered_by_the_pipe_it_left_behind`

## ApiClient

### ApiCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_command_missing_its_argument_names_the_one_it_wanted`
- [T] `A_command_that_wants_a_whole_row_says_which_one`
- `A_tree_crosses_the_wire_under_the_field_names_the_renderer_reads`
- `A_row_sent_back_for_an_update_is_read_under_its_own_field_names`
- `A_null_folder_means_directly_under_the_collection`
- `A_command_that_answers_nothing_answers_null`

### ApiStartupTests

- `The_four_commands_the_app_issues_on_launch_all_answer`
- `A_workspace_with_nothing_in_it_loads_an_empty_tree`
- `A_workspace_gets_its_globals_environment_on_the_next_launch_not_on_creation`
- `A_second_launch_does_not_seed_a_second_globals`

### ApiStoresTests

- `Globals_sorts_ahead_of_every_environment_added_later`
- `An_update_cannot_make_an_environment_global`
- `Deleting_globals_does_nothing`
- `An_ordinary_environment_can_be_deleted`
- `Duplicating_globals_yields_an_ordinary_environment`
- `History_comes_back_newest_first_and_honours_the_limit`
- `Adding_the_same_entry_twice_keeps_one_row`
- `An_entry_with_no_timestamp_is_stamped_on_the_way_in`
- `Clearing_history_leaves_another_workspaces_alone`
- `The_same_cookie_sent_again_replaces_it_rather_than_accumulating`
- [T] `A_cookie_differing_in_any_part_of_its_key_is_a_different_cookie`
- `The_same_cookie_in_two_workspaces_stays_two_cookies`
- `Cookies_are_listed_by_domain_then_path_then_name`

### ApiTreeStoreTests

- `The_whole_tree_comes_back_in_one_call`
- `The_tree_holds_only_this_workspaces_rows`
- `Deleting_a_collection_takes_its_folders_and_requests_with_it`
- `Updating_a_collection_cannot_move_it`
- `Reordering_renumbers_the_sidebar_top_to_bottom`
- `Reordering_with_the_wrong_workspace_changes_nothing`
- `Duplicating_a_collection_deep_copies_it_and_remaps_the_parent_links`
- `A_new_request_takes_its_method_and_url_from_the_spec_it_was_given`
- [T] `A_spec_with_no_method_still_saves_and_falls_back_to_get`
- `Moving_a_request_renumbers_its_new_siblings_densely`
- `An_index_past_the_end_lands_last_rather_than_failing`
- [T] `A_folder_cannot_be_moved_inside_itself_or_its_own_descendant`
- `A_node_cannot_be_moved_into_another_workspaces_collection`
- `Moving_a_folder_between_collections_carries_everything_under_it`
- `An_unknown_node_kind_is_refused_by_name`
- `Duplicating_a_request_lands_beside_its_source`

### HttpDecodingTests

- [T] `A_published_digest_vector_is_reproduced`
- `Auth_parameters_survive_a_quoted_value_containing_commas`
- `A_digest_challenge_hiding_behind_another_scheme_is_not_seen`
- `A_challenge_on_its_own_line_is_found_whatever_the_case`
- `A_challenge_with_no_nonce_cannot_be_answered`
- `An_algorithm_this_client_cannot_compute_says_which_ones_it_can`
- `A_qop_this_client_does_not_implement_is_refused_rather_than_ignored`
- `The_authorization_header_carries_the_query_string_in_its_uri`
- `A_cookie_with_no_attributes_takes_the_host_and_the_root_path`
- `Domain_path_and_expiry_attributes_are_honoured`
- `Max_age_beats_expires`
- `An_expiry_nobody_can_read_is_dropped_rather_than_invented`
- [T] `A_header_that_is_not_a_cookie_yields_nothing`
- `A_declared_text_body_with_one_bad_byte_is_still_shown`
- `A_binary_body_becomes_base64_whether_or_not_it_declared_itself`
- `An_undeclared_text_body_is_text_and_vendor_json_is_recognised`
- `A_declared_latin1_body_is_transcoded_rather_than_replaced`
- [T] `The_charset_parameter_is_read_lowercased_and_unquoted`
- `A_nul_byte_is_what_marks_an_undeclared_body_binary`

### HttpSendTests

- `A_request_reaches_the_server_and_its_response_comes_back_decoded`
- `The_console_reports_what_actually_went_out`
- `A_cookie_jar_is_sent_as_one_header`
- [T] `A_302_turns_any_non_get_into_a_bodiless_get`
- `A_307_keeps_the_method_and_the_body`
- `Each_redirect_hop_is_reported`
- `Redirects_can_be_refused_altogether`
- `Too_many_redirects_says_how_many_were_allowed`
- `A_body_past_the_cap_is_truncated_rather_than_refused`
- `A_binary_response_comes_back_as_base64`
- `A_set_cookie_header_reaches_the_caller_parsed`
- `A_digest_challenge_is_answered_on_a_second_send`
- `A_401_with_no_digest_challenge_says_the_handshake_cannot_continue`
- `A_signed_request_carries_its_authorization_to_the_server`
- [T] `A_url_this_cannot_send_says_why`
- `A_method_that_is_not_one_says_so`
- `A_tracked_request_can_be_cancelled_while_it_is_in_flight`
- `Cancelling_a_request_that_already_finished_is_not_an_error`
- `A_file_body_does_not_stay_open_after_the_send`
- `A_redirected_file_body_leaves_no_hop_open`

### SigV4Tests

- `The_published_aws_get_vanilla_vector_is_reproduced`
- `The_canonical_query_is_sorted_and_encoded_the_way_the_vector_says`
- [T] `The_canonical_path_is_signed_once_for_s3_and_twice_for_everything_else`
- `Characters_the_framework_would_leave_alone_are_encoded`
- `A_repeated_header_is_signed_as_one_comma_joined_value_in_the_order_sent`
- [T] `A_header_value_is_normalised_to_single_spaces`
- [T] `The_headers_the_sdks_exclude_are_excluded_here_too`
- `A_signed_request_carries_the_headers_the_vector_lists`
- `A_session_token_is_carried_and_signed_when_there_is_one`
- [T] `Signing_without_credentials_says_which_half_is_missing`

### StreamCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_command_missing_its_connection_id_says_so`
- [T] `A_connect_command_wants_its_request_object`
- `Disconnecting_a_connection_that_is_not_there_is_a_no_op`
- `Sending_to_a_connection_nobody_opened_says_so`
- `A_websocket_opens_carries_a_frame_both_ways_and_closes`
- `A_socket_opened_with_an_http_url_still_connects`
- `A_binary_frame_arrives_base64_encoded_on_its_own_line`
- `Opening_twice_on_one_id_leaves_one_connection`
- `A_url_that_cannot_be_opened_reports_an_error_and_leaves_nothing_behind`
- `A_connection_that_fails_to_start_is_disposed`
- `A_connection_that_forgot_itself_first_is_still_disposed_exactly_once`
- `A_server_that_closes_ends_the_connection_rather_than_reopening_it`

### StreamFramingTests

- `An_http_url_is_rewritten_to_its_websocket_scheme`
- `Repeated_headers_are_kept_and_singles_replace`
- [T] `A_frame_carries_its_namespace_the_way_the_vector_says`
- `Connect_auth_travels_only_on_v4_and_only_when_there_is_some`
- `One_argument_keeps_its_shape_through_a_round_trip`
- `Several_arguments_become_an_array`
- `A_bare_string_payload_stays_json`
- `An_event_with_no_payload_sends_only_its_name`
- `A_payload_that_is_not_json_is_refused`
- `The_engine_opcodes_decode_and_encode`
- `An_ack_and_a_binary_packet_carry_their_metadata`
- `A_namespace_with_nothing_after_it_still_parses`
- `The_handshake_url_upgrades_its_scheme_and_keeps_the_query`
- `A_broker_address_resolves_its_scheme_and_default_port`
- `An_address_this_cannot_reach_is_refused_rather_than_guessed`
- `A_client_id_is_kept_when_given_and_generated_when_not`
- `A_quality_of_service_out_of_range_falls_to_at_most_once`
- [T] `With_verification_on_only_a_clean_certificate_passes`
- [T] `With_verification_off_a_self_signed_staging_certificate_is_accepted`
- `With_verification_off_a_certificate_that_fails_for_any_other_reason_is_still_refused`

## Dbml

### DbmlAssistantTests

- `The_ask_rides_on_argv_and_the_schema_on_stdin`
- [T] `Each_mode_sends_its_own_system_prompt`
- `A_blank_instruction_is_left_out_of_the_payload_rather_than_sent_as_an_empty_heading`
- `An_instruction_is_sent_after_the_schema`
- `A_long_instruction_is_capped_while_the_schema_is_not_truncated`
- `The_assistant_reaches_for_no_tools`
- `An_edit_answer_loses_the_code_fence_a_model_wraps_it_in`
- `A_review_keeps_the_code_blocks_inside_its_markdown`
- `An_empty_document_is_refused_before_anything_runs`
- `An_edit_with_no_instruction_is_refused_because_it_has_no_meaning`
- [T] `The_other_two_modes_stand_on_their_own_with_no_instruction`
- `A_schema_past_the_cap_is_refused_and_the_error_says_by_how_much`
- `An_unknown_mode_names_itself_in_the_error`

### DbmlCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_missing_argument_is_named_in_the_error`
- `Documents_come_back_project_relative_sorted_and_with_forward_slashes`
- `A_folder_with_no_git_is_listed_like_any_other`
- `Build_and_dependency_directories_are_never_descended_into`
- `The_extension_matches_whatever_case_it_was_written_in_and_nothing_else`
- `An_empty_folder_answers_with_an_empty_list_rather_than_failing`
- `A_folder_that_is_not_there_says_so`
- `A_saved_position_comes_back_with_the_snake_case_keys_it_was_sent_with`
- `Saving_a_table_again_moves_it_rather_than_adding_a_second_row`
- `A_key_repeated_within_one_save_keeps_its_last_position`
- `Each_document_keeps_its_own_positions`
- `Clearing_a_document_forgets_only_that_document`
- `Removing_the_project_removes_its_layouts`
- `An_empty_save_writes_nothing`
- `A_position_without_a_table_key_is_refused_and_nothing_is_written`
- `A_save_for_a_project_that_does_not_exist_fails_on_the_foreign_key`

### DbmlSnapshotBuilderTests

- `A_column_named_by_a_primary_key_constraint_is_marked_pk`
- `Only_a_single_column_unique_constraint_marks_its_column_unique`
- `A_single_column_key_becomes_no_index_because_the_column_already_says_it`
- `A_composite_key_becomes_an_index_because_it_has_nowhere_else_to_live`
- `Key_and_index_members_are_ordered_by_position_not_by_arrival`
- `The_index_backing_a_constraint_is_not_reported_twice`
- `A_composite_foreign_key_keeps_its_columns_paired`
- [T] `Referential_actions_are_normalised_across_the_four_spellings`
- [T] `No_action_is_dropped_because_it_is_what_every_key_that_says_nothing_reports`
- `Tables_and_relations_come_back_in_a_stable_order`
- `Each_schema_keeps_its_own_tables_of_the_same_name`

### ServerIntrospectorTests

- `Postgres_reads_the_schema_it_was_given`
- `MySql_reads_the_schema_it_was_given`
- `SqlServer_reads_the_schema_it_was_given`
- `A_wrong_password_is_a_refusal_that_does_not_repeat_the_password`

### SqliteIntrospectorTests

- `Reads_tables_columns_types_and_nullability`
- `An_integer_primary_key_is_an_increment_because_sqlite_fills_it_in`
- `A_text_primary_key_is_not_an_increment`
- `A_single_column_unique_constraint_marks_the_column_rather_than_making_an_index`
- `A_composite_primary_key_becomes_a_pk_index_in_declared_order`
- `No_member_of_a_composite_key_is_an_increment`
- `Reads_a_foreign_key_with_its_referential_action`
- `A_named_index_comes_back_with_its_name_and_columns`
- `Defaults_lose_the_quoting_sqlite_reports_them_in`
- `Sqlites_own_tables_are_not_part_of_the_users_schema`
- `Reading_a_database_leaves_the_users_file_free_to_delete`
- `A_missing_file_is_refused_rather_than_created`
- `A_connection_with_no_file_says_so_instead_of_opening_something`

## Diagnostics

### ErrorLogTests

- [T] `A_credential_never_reaches_the_file`
- [T] `Everything_that_is_not_a_credential_survives_intact`
- `Redaction_happens_before_the_line_reaches_disk`
- `The_directory_is_the_caller_s_to_choose`

### StartupLogTests

- `The_stage_name_and_the_whole_exception_reach_the_file`
- `A_credential_in_a_startup_failure_is_redacted_like_any_other_line`
- `A_directory_that_cannot_be_written_is_not_a_reason_to_throw`

## Files

### AnchorPatternTests

- `The_pattern_compiles_under_the_options_search_actually_uses`
- [T] `Every_comment_opener_the_editor_recognises_matches_here_too`
- [T] `What_the_editor_refuses_is_refused_here_too`
- `The_tag_comes_back_in_the_capture_group_the_frontend_reads`

### FileCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_command_missing_its_argument_names_the_one_it_wanted`
- `An_export_wants_its_bytes_as_the_array_the_renderer_sends`
- `A_listing_crosses_the_wire_under_the_field_names_the_renderer_reads`
- `A_search_crosses_the_wire_with_snake_case_hits_and_a_truncation_flag`
- `A_null_checkpoint_is_sent_rather_than_omitted`
- `The_search_toggles_are_read_under_their_camel_case_names`

### FileOpsTests

- `Creating_a_nested_path_makes_every_missing_parent_in_one_call`
- `A_move_lands_where_it_was_dropped`
- `A_name_already_taken_at_the_destination_is_refused_rather_than_overwritten`
- [T] `A_folder_cannot_swallow_itself_directly_or_through_a_descendant`
- `Dropping_something_back_where_it_already_lives_is_a_no_op_not_a_failure`
- `Nothing_may_leave_the_repository`
- `An_existing_name_is_reported_back_instead_of_being_silently_truncated`
- `A_whitespace_only_name_is_rejected_as_empty_before_the_component_check`
- `A_creation_naming_a_parent_directory_is_rejected_and_writes_nothing`
- `A_write_to_a_path_that_does_not_exist_yet_is_refused_before_touching_disk`
- `A_folder_asked_for_as_a_file_says_so_instead_of_surfacing_the_os_error`
- `The_tree_lists_directories_first_then_case_insensitively_by_name`
- `A_listing_that_cannot_classify_its_entries_refuses_instead_of_guessing`
- `A_symlink_to_a_directory_is_still_a_directory`
- `A_subdirectory_listing_reports_repo_relative_paths`
- `An_export_needs_an_absolute_path_and_an_existing_folder`
- `An_export_writes_wherever_the_save_dialog_pointed`

### GlobSetTests

- `An_empty_list_filters_nothing_at_all`
- [T] `A_pattern_with_no_slash_matches_by_file_name_at_any_depth`
- [T] `A_star_matches_a_separator_too`
- [T] `A_trailing_double_star_matches_everything_below_a_directory`
- [T] `A_double_star_in_the_middle_matches_zero_or_more_directories`
- `Brace_alternation_is_translated_but_a_comma_inside_it_never_survives_the_list`
- [T] `A_character_class_matches_a_range`
- [T] `A_negated_character_class_matches_what_it_excludes`
- `A_question_mark_stands_for_exactly_one_character`
- `A_comma_separated_list_matches_if_any_pattern_does`
- [T] `A_double_star_that_is_not_a_whole_component_is_rejected`
- `An_unclosed_construct_names_the_pattern_the_user_typed`
- `A_regex_metacharacter_in_a_pattern_is_a_literal`

### RepoWatcherTests

- `Emissions_stop_once_a_write_has_been_reported`
- `A_change_inside_the_window_is_flushed_afterwards_rather_than_dropped`
- `A_burst_is_never_reported_as_nothing`
- `Git_bookkeeping_files_produce_nothing`
- `Starting_twice_on_the_same_repo_does_not_leave_two_watchers`
- `Stopping_a_repo_nobody_watches_is_not_an_error`
- `Stopping_a_watch_stops_its_events`

### SearchTests

- `Listing_prunes_what_gitignore_excludes`
- `Search_is_case_insensitive_by_default`
- `A_case_sensitive_search_matches_only_the_case_it_was_given`
- `Hitting_the_ceiling_is_reported_rather_than_passed_off_as_the_whole_answer`
- `Whole_word_stops_matching_inside_longer_words`
- `Regex_mode_reads_the_query_as_a_pattern_and_literal_mode_does_not`
- `An_unfinished_regex_reports_itself_instead_of_crashing`
- `Include_and_exclude_globs_narrow_the_scan_in_that_order`
- `Replace_rewrites_matches_and_leaves_an_undo_behind`
- `Replace_can_be_scoped_to_one_file_and_can_use_capture_groups`
- `An_empty_query_answers_nothing_without_touching_the_filesystem`
- `A_replace_that_matches_nothing_takes_no_checkpoint`

### TextHandlingTests

- `A_lone_carriage_return_does_not_start_a_new_line`
- [T] `Lines_are_split_the_way_rust_splits_them`
- `A_trailing_newline_does_not_invent_an_empty_last_line`
- `The_line_cap_counts_characters_and_never_splits_a_surrogate_pair`
- `A_line_exactly_at_the_cap_is_left_alone`
- `A_lines_trailing_newline_characters_are_stripped_before_it_is_measured`
- [T] `A_literal_query_matches_itself_and_nothing_cleverer`
- `An_undecodable_file_is_still_searched_but_will_not_open`
- `A_binary_file_is_not_searched_at_all`

### WatcherCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_command_missing_its_argument_names_the_one_it_wanted`
- `Stopping_a_repo_nobody_watches_answers_null_rather_than_failing`
- `A_staged_secret_crosses_the_wire_under_the_field_names_the_renderer_reads`
- `A_clean_staging_area_answers_an_empty_list`
- `An_unstaged_secret_is_not_what_this_commit_introduces`

## Git

### BranchContributionTests

- `A_file_committed_then_edited_again_appears_once_with_its_cumulative_change`
- `An_untracked_file_is_part_of_what_the_branch_contributes`
- `Staged_but_uncommitted_work_counts_too`
- `What_the_base_branch_gained_after_the_fork_is_not_the_branchs_doing`
- `With_a_clean_tree_it_reports_exactly_what_the_committed_branch_diff_does`
- `HeadSha_is_the_checked_out_branchs_own_commit`
- `An_unknown_base_reports_the_branch_by_name`

### BranchesTests

- `Checkout_blocked_by_uncommitted_changes_is_tagged_for_the_ui`
- `A_detached_checkout_blocked_the_same_way_is_tagged_the_same_way`
- `A_checkout_that_fails_for_any_other_reason_is_not_tagged`
- `Checking_out_a_branch_moves_head_and_the_working_tree`
- `A_detached_checkout_leaves_every_branch_pointer_alone`
- `Creating_a_branch_targets_the_start_point_and_does_not_check_it_out`
- `Creating_a_branch_without_a_start_point_targets_head`
- `Creating_a_branch_that_already_exists_fails_rather_than_moving_it`
- `Deleting_the_checked_out_branch_fails_and_is_not_tagged`
- `Deleting_a_branch_removes_it`
- `Ahead_and_behind_are_counted_only_against_a_branchs_own_upstream`
- `Connecting_to_a_remote_branch_creates_a_tracking_branch`
- `Connecting_reuses_an_existing_local_branch_without_repairing_its_upstream`
- `Connecting_needs_a_remote_qualified_name`

### ChangeContextTests

- `A_change_brings_the_whole_declaration_it_sits_in`
- `Only_the_lines_the_change_touched_are_marked`
- `Editing_a_signature_quotes_that_method_and_not_its_class`
- `A_change_outside_every_declaration_is_quoted_too`
- `A_deletion_marks_the_line_that_took_its_place`
- `An_indentation_language_closes_its_block_by_dedenting`
- `A_file_with_no_structure_it_can_read_gets_a_window_and_not_the_file`
- `An_added_file_is_left_to_the_diff_that_already_holds_every_line_of_it`
- `What_the_diff_excludes_is_excluded_here_for_the_same_reasons`
- `A_file_that_cannot_be_given_room_is_named_rather_than_half_shown`
- `Two_changes_in_one_declaration_are_quoted_once`

### CheckpointsTests

- `Restore_reverts_edits_and_deletes_the_files_the_run_created`
- `Taking_a_snapshot_leaves_the_real_index_alone`
- `A_run_that_changed_nothing_drops_its_checkpoint`
- `A_run_that_changed_something_keeps_its_checkpoint`
- `Deleting_a_checkpoint_that_is_not_there_is_not_an_error_but_asking_about_one_is`
- `Restoring_a_checkpoint_that_is_gone_says_so`
- `A_checkpoint_never_shows_up_as_a_branch_or_moves_head`
- `Restoring_twice_is_idempotent`
- `Changed_paths_are_recomputed_against_the_tree_as_it_is_now`
- `Only_the_twenty_newest_checkpoints_are_kept`
- `The_kind_is_carried_through_untranslated`

### CommitGraphTests

- `Commits_come_back_newest_first_with_their_parents_and_refs`
- `A_limit_of_zero_returns_nothing_rather_than_failing`
- `A_repository_with_no_commits_walks_nothing`
- `Head_only_misses_what_another_branch_holds_and_all_refs_finds_it`
- `Unpushed_is_empty_when_there_is_nowhere_to_push_to`
- `A_branch_that_was_never_pushed_reports_everything_the_remote_lacks`
- `Unpushed_is_empty_while_detached`
- `Unpushed_is_what_head_has_and_its_upstream_does_not`

### DiffTests

- `Discard_all_reverts_tracked_edits_and_removes_untracked_files`
- `Discard_all_keeps_staged_content`
- `Discard_all_leaves_conflicted_and_staged_only_paths_alone`
- `Discarding_one_file_restores_it_from_the_index`
- `Staging_a_file_that_is_gone_from_disk_stages_its_removal`
- `Stage_all_and_unstage_all_are_inverses`
- `Unstaging_one_path_resets_only_that_path`
- `Unstaging_without_a_head_fails`
- `The_working_diff_shows_every_line_of_an_untracked_file`
- `A_hunk_carries_full_file_context_with_both_line_numbers`
- `A_staged_rename_is_one_entry_not_two`
- `The_staged_diff_compares_the_index_against_head`
- `A_commit_diff_compares_against_its_first_parent`
- `A_root_commit_diffs_against_nothing`
- `A_committed_rename_is_one_renamed_entry_carrying_both_paths`
- `The_file_list_of_a_commit_names_every_path_without_its_content`
- `A_commit_file_diff_returns_only_the_file_it_was_asked_for`
- `A_renamed_file_stays_renamed_only_when_both_of_its_paths_are_given`
- `Committing_uses_the_explicit_author_only_when_both_halves_are_given`
- `A_half_supplied_author_falls_back_entirely_to_the_configured_identity`
- `Committing_writes_exactly_what_is_staged_and_moves_the_branch`
- `The_commit_message_is_never_rewritten`

### GitCommandsTests

- `All_forty_seven_commands_are_registered_under_their_contract_names`
- [T] `An_error_reaches_the_caller_verbatim_and_unprefixed`
- `A_folder_that_is_not_a_repository_answers_false_instead_of_throwing`
- `A_repository_and_any_folder_inside_it_both_answer_true`
- `A_missing_argument_is_named_in_the_error`
- `Arguments_are_read_by_the_camel_case_names_the_renderer_sends`
- `The_commit_file_commands_read_their_arguments_and_take_an_absent_old_path`
- `Commit_signs_with_the_workspace_identity_of_the_project_at_the_repo_path`
- `An_explicit_author_still_wins_over_the_workspace_identity`
- `Commit_falls_back_to_the_repo_signature_when_no_project_matches_the_path`

### GitNetworkTests

- `Cloning_streams_progress_and_reports_success`
- `A_failed_operation_reports_the_reason_on_both_paths`
- `Fetch_defaults_to_origin_and_updates_the_remote_tracking_refs`
- `Pulling_a_divergent_branch_completes_the_merge_without_an_editor`
- `Pushing_reaches_the_other_repository`
- `Pushing_upstream_from_a_detached_head_refuses`
- `Every_line_of_both_streams_becomes_its_own_event`
- `The_event_payloads_are_spelled_the_way_the_renderer_reads_them`

### IdentityTests

- `Reads_the_same_global_identity_the_git_binary_reports`

### MergeTests

- `Merging_something_already_contained_is_up_to_date`
- `Merging_a_branch_ahead_of_head_fast_forwards_without_a_merge_commit`
- `A_clean_merge_commits_two_parents_and_clears_the_merge_state`
- `A_clean_merge_signs_with_the_supplied_author_when_both_halves_are_given`
- `A_conflicting_merge_returns_conflicts_as_a_result_not_an_error`
- `The_three_conflicting_sides_are_readable_from_the_index`
- `A_side_that_does_not_exist_reads_as_empty_rather_than_failing`
- `Taking_one_side_writes_it_to_disk_and_clears_the_conflict`
- `Marking_resolved_stages_whatever_the_user_left_on_disk`
- `Completing_refuses_while_conflicts_remain`
- `Completing_commits_both_parents_and_ends_the_merge`
- `Completing_signs_with_the_supplied_author_when_both_halves_are_given`
- `Completing_still_works_after_the_process_restarted_mid_conflict`
- `Aborting_restores_head_and_ends_the_merge`
- `Aborting_a_clean_uncommitted_merge_also_discards_it`
- `A_local_branch_wins_over_a_remote_one_with_the_same_name`

### PromptDiffTests

- `A_lone_change_in_a_long_file_brings_its_neighbours_and_not_the_file`
- `The_lines_it_leaves_out_are_counted_where_they_were`
- `Each_run_says_where_it_starts_so_a_finding_can_cite_a_real_line`
- `Two_distant_changes_stay_two_runs_with_the_gap_between_them_declared`
- `Adjacent_changes_are_one_run_rather_than_a_gap_of_nothing`
- `An_added_file_is_shown_whole_because_all_of_it_is_new`
- [T] `Churn_with_no_reviewable_signal_is_named_rather_than_shown`
- `A_directory_that_is_only_usually_build_output_is_still_reviewed`
- `A_tight_budget_still_shows_the_last_file_rather_than_only_the_first`
- `A_file_that_does_not_fit_is_cut_on_a_line_and_says_it_was_cut`
- `A_file_with_no_room_left_is_named_instead_of_half_shown`
- `Many_files_that_all_fit_are_all_shown`
- `A_single_line_gap_is_shown_rather_than_announced`
- `Absorbing_a_gap_leaves_one_run_and_therefore_one_anchor`
- `A_gap_wide_enough_to_pay_for_itself_is_still_announced`
- `A_diff_that_fits_carries_no_notice_at_all`
- `A_deleted_file_is_reported_under_the_path_it_had`
- `A_binary_file_still_announces_that_it_changed`
- `Nothing_changed_renders_nothing`
- `Everything_fits_when_there_is_room_for_everything`
- `What_a_small_file_does_not_need_goes_to_the_ones_that_do`
- `No_budget_means_no_shares_rather_than_a_crash`
- `A_share_never_exceeds_what_the_file_costs`

### RemotesTests

- `A_repository_with_no_remotes_lists_none`
- `Setting_a_url_writes_both_the_fetch_and_the_push_url`
- `Every_configured_remote_is_listed`

### RepoStatusTests

- `A_path_staged_and_then_modified_again_is_reported_once_as_staged`
- `Every_bucket_gets_its_own_label`
- `A_staged_rename_is_reported_as_renamed`
- `An_ignored_file_is_not_reported_at_all`
- `A_repository_with_no_commits_has_no_current_branch`
- `A_detached_head_reports_no_branch`
- [T] `Reset_moves_head_and_the_mode_decides_what_follows`
- `Resetting_to_an_unknown_commit_fails`

### StashTests

- `Renaming_the_top_stash_keeps_the_order`
- `Renaming_a_lower_stash_moves_it_to_the_top`
- `Renaming_a_stash_that_is_not_there_says_so`
- `Saving_defaults_the_message_and_can_include_untracked_files`
- `Untracked_files_stay_put_when_they_are_not_asked_for`
- `Applying_keeps_the_stash_and_popping_removes_it`
- `A_pop_that_conflicts_says_so_and_keeps_the_stash`
- `A_conflicted_stash_leaves_the_repository_out_of_a_merge`
- `Dropping_removes_a_stash_without_applying_it`
- `Stashing_unblocks_a_checkout_that_local_changes_were_blocking`

### UnifiedDiffTests

- `Every_file_in_the_diff_comes_back`
- `The_git_prefixes_are_stripped_so_the_path_is_the_repositorys_own`
- `Lines_keep_their_origin_and_their_numbers`
- `An_added_file_reports_no_old_path`
- `A_deleted_file_reports_no_new_path`
- `A_binary_file_is_reported_even_though_it_has_no_hunks`
- `A_removed_line_that_looks_like_a_header_stays_content`
- `Nothing_in_means_nothing_out`
- `A_parsed_diff_gets_the_same_trimming_as_a_local_one`
- `A_parsed_diff_respects_the_budget`
- `A_shape_the_parser_does_not_recognise_is_truncated_and_says_so`
- `A_short_unrecognised_diff_is_passed_through_untouched`
- `Nothing_renders_nothing`

## Ipc

### FrameCodecTests

- `Round_trips_a_single_frame`
- `Reads_several_frames_delivered_in_one_chunk`
- `Reassembles_a_frame_split_across_reads`
- `Round_trips_an_empty_payload`
- `Returns_null_when_the_peer_closes_cleanly`
- `Throws_when_the_peer_closes_mid_frame`
- `Rejects_an_implausible_length_prefix`
- `Refuses_to_write_a_frame_beyond_the_limit`

### IpcServerTests

- `Dispatches_a_command_and_returns_its_result`
- `Reports_an_unknown_command_as_an_error_rather_than_dropping_it`
- `Surfaces_a_handler_exception_verbatim`
- `Answers_out_of_order_so_a_slow_command_does_not_block_the_next`
- `Rejects_a_connection_with_the_wrong_token`
- `Publishing_without_a_stream_channel_is_a_no_op`
- `Delivers_an_event_on_the_stream_channel`

### NamedPipeIpcListenerTests

- [T] `The_pipe_name_is_the_endpoint_without_its_namespace`
- `The_published_endpoint_can_be_opened_by_the_path_it_names`

## Platform

### TransientNetworkTests

- [T] `A_connection_that_never_got_made_is_worth_repeating`
- [T] `Everything_else_is_left_alone`
- `Nothing_is_not_a_transient_failure`
- `The_reason_is_found_however_deeply_dotnet_buried_it`
- `A_chain_with_nothing_transient_in_it_is_not_transient`

### TransientRetryHandlerTests

- `A_read_that_could_not_resolve_its_host_is_sent_again`
- `The_repeat_carries_the_headers_the_first_attempt_had`
- `A_write_is_never_repeated`
- `A_failure_that_is_not_the_network_is_not_retried`
- `An_outage_that_outlasts_the_retry_reports_the_original_reason`

## Providers

### AzureClientTests

- `Every_request_authenticates_as_basic_with_an_empty_user_and_the_pat_as_the_password`
- `No_request_carries_an_accept_or_user_agent_header_unlike_the_github_client`
- `Every_endpoint_pins_the_ga_contract`
- `Connection_data_is_the_one_endpoint_on_the_preview_contract`
- [T] `An_organisation_saved_in_any_of_its_three_forms_reduces_to_the_bare_name`
- `An_unrecognised_organisation_string_is_assumed_to_already_be_a_name`
- `The_legacy_host_suffix_is_matched_case_sensitively`
- `A_url_shaped_organisation_never_reaches_the_path_with_its_colon_intact`
- [T] `A_path_segment_keeps_only_the_unreserved_set_and_escapes_the_rest_in_upper_case_hex`
- `The_repository_is_encoded_when_a_single_pull_request_is_fetched`
- `The_repository_is_sent_raw_when_the_list_is_fetched_and_a_reserved_character_breaks_the_url`
- `Listing_pull_requests_asks_for_every_status_in_one_call_and_sets_no_page_size`
- `A_pull_request_maps_onto_the_shared_summary_shape`
- `The_browser_url_is_synthesised_because_the_api_returns_no_page_a_person_can_open`
- [T] `Azures_status_vocabulary_collapses_into_the_four_buckets_the_sidebar_groups_by`
- `An_abandoned_draft_is_closed_rather_than_draft`
- `A_missing_description_reads_as_empty_rather_than_failing`
- `Fetching_one_pull_request_recovers_the_names_a_guid_carrying_link_never_had`
- `A_response_with_no_project_falls_back_to_the_project_that_was_asked_about`
- `Creating_a_pull_request_adds_the_full_ref_prefix_azure_requires`
- `An_already_prefixed_branch_is_doubled_rather_than_detected`
- [T] `Azures_five_point_vote_collapses_into_the_three_strings_the_ui_reads`
- `Reading_a_decision_asks_for_the_identity_first_and_then_the_pull_request`
- `A_reviewer_id_matches_without_regard_to_case`
- `A_viewer_who_is_not_a_reviewer_has_decided_nothing`
- `A_list_response_carrying_no_reviewers_is_read_as_no_vote`
- `Casting_a_vote_puts_it_on_the_reviewer_resource_which_also_adds_the_reviewer`
- `Abandoning_a_pull_request_patches_its_status`
- `An_anchored_thread_carries_its_file_and_line_range_through_untouched`
- [T] `Only_threads_azures_own_ui_still_treats_as_open_are_kept`
- `A_thread_with_no_status_at_all_counts_as_open`
- [T] `Only_comments_a_person_wrote_survive`
- `A_thread_left_with_nothing_readable_disappears_entirely`
- `A_conversation_thread_carries_no_anchor`
- `The_latest_iteration_is_the_last_one_listed`
- `An_empty_iteration_list_falls_back_to_the_first_rather_than_failing`
- `The_iteration_lookup_sends_the_repository_raw_even_though_its_caller_encodes_it`
- [T] `A_status_that_is_not_about_the_credential_still_collapses`
- [T] `A_sign_in_page_is_summarised_instead_of_becoming_the_error_message`
- `A_json_error_from_the_api_is_still_reported_verbatim`
- [T] `A_refused_credential_is_told_apart_from_everything_else`
- `A_transport_failure_says_it_could_not_reach_the_host`
- `A_body_that_is_not_the_expected_json_says_the_response_was_unexpected`
- `Listing_projects_unwraps_the_value_envelope`
- `Listing_repositories_is_scoped_to_one_project`

### AzureDiffTests

- `A_modified_file_renders_as_a_unified_diff_naming_its_real_path`
- `The_change_list_is_read_against_the_base_of_the_whole_pull_request`
- `A_blob_is_read_as_an_octet_stream_because_a_file_is_not_text`
- `A_file_reads_its_base_side_before_its_target_side`
- `An_added_file_reads_only_its_target_side`
- `A_deleted_file_reads_only_its_base_side`
- [T] `A_placeholder_object_id_counts_as_no_blob_at_all`
- `A_folder_entry_is_skipped_entirely`
- `An_entry_with_no_item_is_skipped_rather_than_failing`
- `A_file_whose_blob_cannot_be_read_is_listed_as_unreadable_and_the_rest_stands`
- `An_oversized_blob_is_listed_and_downloaded_anyway`
- `The_change_type_is_quoted_back_in_the_placeholder_in_lower_case`
- `Past_eighty_files_the_rest_are_dropped_and_the_count_is_stated`
- `Exactly_eighty_files_carry_no_note`
- `Files_keep_the_order_azure_listed_them_in_however_they_finish`
- `A_pull_request_with_no_changes_at_all_is_an_error_rather_than_an_empty_diff`
- `A_change_list_whose_files_all_render_to_nothing_is_the_same_error`
- `An_unreadable_file_is_still_a_diff_because_the_placeholder_is_content`

### AzurePostingTests

- `An_anchored_thread_reads_the_latest_iteration_first_and_returns_its_id`
- `The_anchored_body_carries_the_comment_the_range_and_the_iteration_window`
- [T] `The_path_gains_a_leading_slash_when_it_has_none`
- `An_inverted_range_ends_on_whichever_line_is_higher`
- `A_plain_thread_omits_both_context_blocks`
- `A_reply_posts_under_the_hardcoded_first_comment`
- `Marking_a_thread_fixed_patches_its_status`
- [T] `The_repository_id_goes_into_the_url_unencoded`
- `A_rejected_thread_reports_the_status_and_the_body`
- `A_created_thread_that_will_not_parse_has_its_own_wording`

### AzureWorkItemClientTests

- `Every_query_names_the_project_in_its_where_clause`
- `An_extra_condition_is_anded_with_the_project_clause_not_substituted_for_it`
- `A_project_name_carrying_an_apostrophe_is_escaped_for_the_literal`
- `A_query_returns_the_ids_whatever_the_select_asked_for`
- `A_query_that_matched_nothing_is_an_empty_list_not_a_failure`
- `Comments_are_pinned_to_the_preview_contract`
- `Adding_a_comment_posts_its_html_to_the_same_preview_contract`
- `An_iterations_work_item_list_is_pinned_to_its_own_preview_contract`
- `A_batch_larger_than_azures_ceiling_is_split_rather_than_rejected`
- `A_batch_omits_unreadable_ids_instead_of_failing_the_whole_list`
- `An_empty_batch_asks_nothing_of_the_network`
- `A_summary_batch_asks_only_for_the_fields_a_row_shows`
- `A_work_items_fields_survive_their_dots_and_their_custom_names`
- `The_parent_and_the_attachments_come_from_relations_not_from_fields`
- `Reading_one_work_item_asks_for_everything_it_has`
- `An_iterations_work_items_are_read_out_of_its_relations`
- `A_refused_credential_is_marked_the_same_way_the_pull_request_client_marks_it`
- `A_project_name_with_spaces_is_percent_encoded_into_the_path`
- `An_attachment_is_downloaded_from_the_url_its_relation_carries`
- `The_web_url_is_the_modern_form_a_person_would_recognise`
- `An_organisation_saved_as_a_url_still_produces_a_usable_path`

### GitHubClientTests

- [T] `The_api_root_splits_github_com_from_every_enterprise_host`
- `Every_request_carries_the_four_headers_the_api_requires`
- `An_enterprise_host_is_reached_on_its_own_api_path`
- `A_transport_failure_says_it_could_not_reach_github`
- [T] `Every_status_produces_the_same_error_shape`
- `A_body_that_is_not_the_expected_shape_says_the_response_was_unexpected`
- `Listing_asks_for_every_state_newest_first_and_maps_each_pull`
- `A_pull_request_with_no_description_maps_to_an_empty_one`
- [T] `The_four_buckets_collapse_in_the_right_order`
- `A_pull_request_with_no_head_commit_is_refused_rather_than_anchored_to_nothing`
- `The_diff_is_asked_for_as_a_diff_and_returned_verbatim`
- [T] `A_refused_or_blank_diff_falls_back_to_the_changed_files`
- `The_reassembled_headers_name_real_paths_even_where_the_markers_say_dev_null`
- `The_fallback_stops_paging_as_soon_as_a_page_is_not_full`
- `A_pull_request_with_no_changed_files_at_all_is_an_error`
- [T] `The_last_verdict_wins_and_only_a_verdict_counts`
- `Another_reviewers_verdict_is_not_mistaken_for_the_users_own`
- `Creating_a_pull_request_sends_exactly_the_five_fields_the_api_takes`
- `An_approval_with_no_comment_omits_the_body_key_entirely`
- `A_review_with_a_comment_carries_it`
- `Closing_a_pull_request_patches_its_state`
- `Replies_join_the_thread_they_answer_and_roots_keep_their_first_seen_order`
- `A_single_line_thread_reports_the_same_line_at_both_ends`
- `Empty_comments_are_dropped_and_the_rest_are_trimmed`
- `Conversation_comments_become_their_own_location_less_threads_after_the_inline_ones`
- [T] `A_status_that_is_not_the_self_approval_rule_still_collapses`
- `A_422_that_is_not_about_self_approval_stays_undifferentiated`
- `Approving_your_own_pull_request_is_told_apart_from_everything_else`
- `The_self_approval_sentence_is_matched_regardless_of_capitalisation`

### GitHubPostingTests

- `An_anchored_comment_posts_to_the_review_comments_endpoint`
- `A_single_line_comment_omits_the_range_start`
- `A_multi_line_comment_carries_the_range_start`
- `An_inverted_range_anchors_to_whichever_line_is_higher`
- `The_path_loses_its_leading_slash`
- `A_general_comment_goes_to_the_issues_endpoint`
- `A_reply_threads_off_the_root_comments_id`
- `An_enterprise_host_writes_through_its_own_api_root`
- `Resolving_a_thread_finds_it_by_database_id_and_then_mutates_it`
- `The_graphql_calls_drop_the_two_rest_only_headers`
- `A_thread_that_holds_no_matching_comment_is_reported_as_not_found`
- `A_response_with_no_threads_node_says_so`
- [T] `A_graphql_failure_names_which_call_failed_and_omits_the_body`
- `A_batch_whose_review_analysed_an_older_head_is_refused`
- `The_early_check_refuses_before_a_summary_could_be_posted`
- `The_early_check_asks_nothing_when_no_finding_is_anchored`
- `The_early_check_asks_nothing_when_the_run_recorded_no_head`
- `The_early_check_lets_a_matching_head_through`
- `A_finding_the_diff_cannot_hold_is_posted_unanchored_rather_than_lost`
- `A_refusal_that_is_not_about_the_anchor_still_fails`
- `The_head_is_asked_for_once_even_when_the_gate_ran_first`
- `A_batch_whose_head_still_matches_posts_normally`
- `A_run_that_recorded_no_head_is_posted_rather_than_blocked`
- `A_conversation_only_batch_is_never_blocked_by_a_moved_head`
- `GitHubs_conversation_runs_oldest_first_so_the_summary_is_posted_first`

### LinkedRepoTests

- `A_github_linked_project_resolves_to_github`
- `An_explicit_enterprise_host_is_carried_through`
- `An_ado_linked_project_resolves_to_azure`
- `Github_wins_when_a_project_carries_both_links`
- `A_half_filled_link_does_not_count`
- `An_unlinked_project_says_so_in_the_words_the_frontend_shows`
- `Linking_one_provider_leaves_the_other_columns_alone`
- `Unlinking_clears_all_six_columns_whichever_was_set`
- `Every_auto_link_variant_carries_the_discriminator_the_renderer_switches_on`
- `Every_pr_link_variant_carries_the_discriminator_the_renderer_switches_on`
- `A_variants_own_fields_stay_snake_case_while_its_tag_stays_pascal_case`
- `A_ready_resolution_names_the_project_the_way_the_renderer_reads_it`

### PrDescriptionDraftTests

- `The_title_line_is_lifted_out_and_the_body_keeps_the_rest`
- `An_indented_marker_still_counts`
- `The_marker_is_case_sensitive`
- `A_marker_that_is_not_the_first_line_is_still_found_and_the_lines_above_it_are_kept`
- `Only_the_first_marker_is_consumed`
- `No_marker_at_all_leaves_the_title_empty_rather_than_guessing_one`
- `An_empty_title_after_the_marker_is_an_empty_title_not_a_missing_one`
- `Everything_empty_stays_empty`

### PrLinkTests

- [T] `Parse_matches_the_extracted_vector`
- `Every_vector_case_is_exercised`

### ProviderIpcTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `Linking_a_project_by_hand_and_unlinking_it_round_trip_through_the_wire`
- `Auto_linking_a_repo_with_no_recognisable_remote_reports_that_rather_than_failing`
- `Auto_linking_a_github_remote_with_no_saved_token_asks_for_one`
- `The_repository_web_url_is_rebuilt_from_the_remote_not_from_the_stored_link`
- `A_repository_whose_remote_is_not_a_known_host_says_so`
- `An_unlinked_project_is_refused_in_the_words_the_frontend_shows`
- `An_azure_linked_project_with_no_saved_pat_names_the_organisation_to_connect`
- `Linking_a_project_to_azure_by_hand_writes_only_its_own_three_columns`
- `The_manual_dialogs_project_lookup_needs_a_pat_and_says_so`
- `An_azure_pull_request_reaches_the_panel_over_the_wire`
- `Approving_an_azure_pull_request_votes_and_files_the_decision_in_activity`
- `An_azure_link_with_no_saved_pat_asks_for_the_organisation_rather_than_the_host`
- `An_azure_link_whose_saved_pat_is_refused_says_so_rather_than_asking_to_connect`
- `Listing_pull_requests_marks_a_refused_credential_for_the_sidebar`
- `Approving_your_own_pull_request_is_marked_for_the_toast`
- `A_pull_request_action_that_fails_for_any_other_reason_carries_no_prefix`
- `An_azure_link_that_fails_for_any_other_reason_still_fails_as_an_error`
- `An_azure_link_with_no_matching_local_repo_offers_a_clone_url_without_a_git_suffix`
- `An_azure_link_binds_the_local_project_whose_remote_points_at_it`
- `A_pasted_link_that_is_not_a_pull_request_resolves_rather_than_erroring`
- `A_pasted_link_for_a_host_with_no_token_asks_for_one_and_names_the_host`
- `The_pr_link_commands_throw_on_an_unreadable_link_instead_of_reporting_a_state`
- `A_linked_project_with_no_saved_token_names_the_host_and_points_at_settings`
- `Asking_who_a_token_belongs_to_without_one_uses_the_other_wording`
- `A_missing_parameter_is_named_rather_than_crashing_the_dispatch`

### RepoDetectionTests

- [T] `Every_shape_a_github_remote_comes_in_resolves_to_the_same_repo`
- `An_unknown_host_is_not_assumed_to_be_github_enterprise`
- `The_detected_host_takes_the_spelling_that_was_saved`
- `A_deeper_github_path_keeps_only_the_first_two_segments`
- [T] `A_remote_with_no_owner_and_repo_is_not_recognised`
- `The_modern_azure_https_remote_resolves`
- `The_legacy_visualstudio_remote_resolves_with_and_without_the_collection`
- `The_one_azure_ssh_shape_is_an_exact_prefix_and_exactly_three_segments`
- `An_azure_path_that_is_not_exactly_the_expected_shape_is_rejected`
- `An_azure_remote_with_no_scheme_is_rejected`
- `The_legacy_organisation_keeps_the_hosts_own_casing`
- `The_legacy_host_suffix_is_matched_case_sensitively`
- `A_github_remote_is_not_mistaken_for_an_azure_one_and_the_reverse`
- `Every_trailing_git_suffix_is_removed_not_just_one`

### UnifiedPatchTests

- [T] `The_extracted_vectors_render_as_the_reference_renders_them`
- [T] `A_side_that_does_not_exist_still_renders_as_a_modification`
- `A_file_that_did_not_change_renders_empty_rather_than_null`
- `A_path_with_a_space_keeps_it_in_the_header`
- `The_temporary_repository_does_not_outlive_the_call`

### WorkItemLinkTests

- [T] `A_work_item_page_gives_up_all_three_parts`
- `A_taskboard_url_is_read_from_its_query_not_its_path`
- `A_project_name_with_percent_encoded_spaces_comes_back_decoded`
- [T] `A_bare_id_is_accepted_and_leaves_the_rest_to_be_filled_in`
- `An_organisation_scoped_link_has_no_project_to_report`
- [T] `Anything_that_is_not_a_work_item_is_refused`

## Review

### ReviewCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `Marking_a_finding_a_false_positive_stores_the_state_and_the_reason`
- `A_blank_reason_is_dropped_rather_than_stored`
- `Un_marking_a_finding_that_was_never_posted_returns_it_to_open`
- `Un_marking_a_finding_that_has_a_thread_returns_it_to_posted`
- `Marking_a_finding_nobody_stored_says_so`
- `Marking_inside_a_run_nobody_stored_says_so`
- `Getting_a_run_nobody_stored_answers_null_rather_than_failing`
- `A_run_exports_as_a_folder_of_four_files`
- `Exporting_a_run_nobody_stored_writes_nothing_rather_than_failing`

### ReviewFromLinkTests

- `The_no_clone_warning_is_the_first_context_the_model_sees`
- `An_agents_own_instructions_frame_the_review_ahead_of_the_warning`
- `The_working_directory_holds_the_pull_request_and_its_diff`
- `A_pull_request_with_no_description_says_so_in_spanish`
- `Reviewing_the_same_link_twice_reuses_its_directory_and_remembers_nothing`
- `A_link_nothing_recognises_fails_before_any_network_call`

### ReviewMemoryParseTests

- `A_header_yields_severity_type_category_and_the_models_own_id`
- [T] `The_word_in_the_brackets_is_what_decides_the_severity`
- [T] `An_unrecognised_severity_word_falls_back_to_the_emoji`
- `A_severity_the_model_contradicts_with_its_own_emoji_follows_the_word`
- `Every_field_is_read_from_its_own_block`
- `The_subtitle_skips_the_structured_fields`
- `A_block_with_nothing_but_structured_fields_has_an_empty_subtitle`
- [T] `A_location_with_a_line_number_splits_into_file_and_lines`
- [T] `A_location_with_no_line_number_stays_a_bare_file`
- `The_unaccented_spelling_of_ubicacion_is_accepted_too`
- `An_unparsable_confidence_is_simply_absent`
- `A_fresh_parse_carries_the_sentinel_iteration_and_the_open_state`
- `Text_that_is_not_a_finding_header_yields_nothing`
- `Windows_line_endings_still_match_the_header`
- `Identity_normalises_the_leading_slash_and_the_case`

### ReviewMemoryReconcileTests

- `An_unmatched_current_finding_is_new`
- `A_matching_finding_persists_and_keeps_everything_the_previous_run_knew`
- `A_persisting_finding_from_before_iteration_tracking_gets_a_plausible_one`
- `A_pre_tracking_row_on_the_very_first_iteration_still_gets_at_least_one`
- `A_persisting_finding_that_a_human_discarded_keeps_that_mark_and_is_not_counted`
- `A_finding_that_reappears_after_being_resolved_is_treated_as_brand_new`
- `An_active_previous_finding_that_did_not_resurface_is_resolved`
- `On_an_efficient_re_review_a_finding_whose_file_was_not_touched_persists_instead`
- `A_finding_with_no_location_always_counts_as_re_analysed`
- [T] `The_changed_file_match_is_suffix_tolerant`
- `An_already_resolved_previous_finding_is_carried_forward_untouched`
- `Two_findings_that_share_a_file_and_a_category_get_one_previous_row_each`
- `An_active_previous_row_is_preferred_over_a_resolved_one_with_the_same_identity`
- `A_finding_with_neither_a_location_nor_a_category_falls_back_to_its_subtitle`
- `A_finding_with_a_category_but_no_file_does_not_fall_back`
- `The_delta_reports_the_two_iterations_it_compared`
- `A_shallower_re_review_does_not_call_an_unexamined_finding_resolved`
- `A_re_review_at_the_same_depth_or_deeper_still_resolves`
- `A_finding_stored_before_levels_existed_behaves_exactly_as_it_did`
- `A_re_found_finding_records_the_depth_that_just_saw_it`
- `The_banner_only_mentions_out_of_scope_when_there_is_some`

### ReviewMemoryRenderTests

- `There_is_no_history_section_when_nothing_was_resolved_or_discarded`
- `Resolved_findings_render_the_traceability_history`
- `A_resolved_finding_with_no_location_renders_a_dash`
- `Discarded_findings_render_their_own_section_with_the_reason`
- `Both_sections_render_together_resolved_first`
- `The_delta_banner_ends_in_a_blank_line_because_it_is_prepended_onto_the_body`
- `An_open_finding_this_run_never_restated_is_named_rather_than_only_counted`
- `A_finding_the_body_already_carries_is_not_repeated_underneath_it`
- `A_resolved_finding_belongs_to_the_history_and_not_to_the_open_list`

### ReviewMemoryRenumberTests

- `The_headers_take_the_ids_reconciliation_assigned`
- `Nothing_but_the_id_moves`
- `An_id_mentioned_in_prose_is_left_alone`
- `A_mapping_that_does_not_line_up_changes_nothing`
- `More_reconciled_findings_than_headers_is_normal_and_fine`
- `A_review_with_no_findings_is_returned_as_it_came`

### ReviewPostingFromLinkTests

- `Every_finding_opens_a_fresh_thread_every_time`
- `A_finding_with_no_location_still_posts_as_a_plain_comment`
- `The_summary_rides_along_as_its_own_comment`
- `A_link_nothing_recognises_fails_before_any_network_call`

### ReviewPostingTests

- `An_unposted_finding_opens_a_thread_and_is_recorded_as_posted`
- `A_finding_that_already_has_a_thread_gets_a_follow_up_instead`
- `A_resolved_finding_is_replied_to_and_its_thread_marked_fixed`
- `A_finding_that_was_already_resolved_stays_resolved_when_it_is_first_posted`
- `A_finding_with_no_location_posts_as_a_conversation_comment`
- `Two_findings_that_collide_on_identity_each_open_their_own_thread`
- `An_item_matching_no_stored_finding_posts_and_leaves_no_record`
- `A_run_nobody_stored_posts_everything_as_new`
- `On_azure_the_summary_is_posted_last_so_that_it_reads_first`
- `A_summary_that_was_never_drafted_is_not_posted`
- `Whatever_did_post_is_remembered_even_when_the_batch_partly_failed`
- `A_failed_summary_is_reported_under_its_own_label`
- `The_runs_recorded_head_is_never_consulted_before_anchoring`

### ReviewRunStoreTests

- `A_run_is_idempotent_by_id`
- `Only_findings_can_be_changed_once_a_run_is_written`
- `The_latest_head_comes_from_the_newest_run_by_creation_time`
- `A_run_with_no_recorded_head_answers_nothing`
- `A_pr_with_no_runs_answers_nothing`
- `The_listing_joins_the_project_name_and_reads_the_title_out_of_the_runs_own_meta`
- `A_run_whose_meta_carries_no_title_lists_with_an_empty_one`
- `Deleting_a_project_takes_its_runs_with_it`
- `Deleting_one_prs_history_leaves_the_others_alone`
- `Purging_a_workspace_wipes_only_that_workspaces_runs`
- `Moving_a_project_moves_its_review_history_with_it`

### ReviewRunTests

- `The_coverage_line_cannot_be_read_as_saying_the_opposite`
- `A_change_that_reached_the_model_whole_says_only_that`
- `Everything_left_out_is_named_with_its_reason`
- `A_first_review_saves_a_run_and_files_it_in_the_activity_list`
- `Reviewing_the_same_commit_again_returns_without_calling_the_model`
- `A_re_review_of_a_new_commit_prepends_the_delta_banner`
- `A_finding_that_stops_being_reported_is_resolved_and_rendered_in_the_history`
- `A_run_the_user_stopped_leaves_nothing_behind`
- `A_failed_run_is_filed_as_an_error_but_saves_no_memory`
- `A_pull_request_the_host_does_not_list_is_reported_as_missing`

## Security

### CredentialStoreTests

- `Key_formats_are_reproduced_byte_for_byte`
- `Reading_a_key_that_was_never_stored_returns_null_rather_than_failing`
- `Stores_reads_and_deletes_a_secret`
- `Deleting_something_that_is_not_there_succeeds`
- `Round_trips_a_secret_with_non_ascii_content`
- `A_stored_secret_is_visible_to_a_separate_process`
- `A_refused_read_is_reported_as_something_the_user_can_act_on`

### SecretCommandsTests

- [T] `No_command_hands_a_credential_back_to_the_renderer`
- [T] `Every_credential_family_offers_set_has_and_delete`
- `The_credential_surface_is_exactly_nine_commands`

### SecretScanTests

- [T] `Every_extracted_vector_is_reproduced`
- `A_line_matching_two_rules_is_reported_under_the_first_one`
- `At_most_one_hit_is_reported_per_line`
- `A_removed_line_is_not_what_this_commit_introduces`
- `A_file_with_no_new_path_falls_back_to_its_old_one_and_then_to_a_question_mark`
- `A_line_libgit2_gave_no_number_for_is_reported_as_zero`
- [T] `The_mask_hides_a_short_value_entirely`
- `The_mask_never_shows_more_than_sixteen_bullets`
- `The_mask_measures_characters_and_not_utf16_units`
- [T] `A_template_value_is_not_a_secret`
- `The_placeholder_check_applies_only_to_the_generic_rule`

## Storage

### MigrationTests

- `A_fresh_database_gets_every_table_and_index`
- `A_fresh_database_needs_no_api_table_migration`
- `Migrating_a_pre_workspace_database_keeps_every_row_and_reparents_it`
- `Running_the_migration_twice_changes_nothing`
- `A_database_with_no_workspace_keeps_its_legacy_rows_until_one_exists`
- `A_launch_after_a_half_applied_copy_finishes_the_job_instead_of_colliding`
- `Every_workspace_is_seeded_with_both_built_in_prompts`
- `An_unedited_seeded_prompt_is_refreshed_to_the_current_built_in_while_an_edited_one_is_kept`
- `An_existing_workspaces_table_gains_the_git_identity_pair`
- `An_existing_workspaces_table_gains_the_ticket_account_pair`
- `A_review_run_stranded_by_a_pre_fix_move_is_realigned_with_its_project`
- `Foreign_keys_cascade_a_workspace_deletion`

## Terminal

### ShellResolverTests

- `A_unix_session_runs_whatever_shell_the_user_has`
- [T] `A_unix_session_with_no_shell_set_falls_back_to_bash`
- `A_windows_session_is_git_bash_as_a_login_shell`
- `A_windows_machine_without_git_bash_refuses_rather_than_substituting_a_shell`
- `The_refusal_message_is_the_reference_string`

### TerminalCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `Writing_to_a_session_nobody_opened_says_so_over_the_wire`
- `Closing_a_session_nobody_opened_answers_null`
- [T] `A_command_missing_its_argument_names_the_one_it_wanted`
- `Resize_needs_both_of_its_numbers`

### TerminalSessionTests

- `A_shell_runs_a_command_and_its_output_arrives_tagged_with_the_session_id`
- `Leaving_the_shell_reports_exactly_one_exit_and_it_comes_last`
- `Closing_a_session_stops_its_shell_and_reports_the_exit`
- `A_session_can_be_resized_after_it_opens`
- `Writing_to_a_session_nobody_opened_says_so`
- `Resizing_a_session_nobody_opened_says_so`
- `Closing_a_session_nobody_opened_is_not_an_error`

## TestVectors

### FixtureCatalogTests

- `The_catalog_directory_is_found_and_populated`
- [T] `Every_fixture_declares_the_schema_and_carries_cases`
- [T] `Scenario_fixtures_carry_their_seed_artefact`
- [T] `Every_case_is_identifiable`
- `Every_case_group_named_by_a_fixture_is_unique_to_it`

## Tickets

### AzureBoardsEndToEndTests

- `The_saved_pat_can_list_the_organisations_projects`
- `A_query_carrying_the_project_clause_returns_that_projects_work_items`
- `Whether_an_unfiltered_query_returns_anything_is_organisation_dependent`
- `A_sprints_work_items_come_back_through_the_taskboard_route`
- `A_real_work_item_reads_back_with_its_fields_and_relations`
- `Comments_are_readable_only_on_the_preview_contract`
- `A_ticket_synced_from_the_real_board_produces_a_readable_mirror`
- `A_single_work_item_previews_fast_enough_to_resolve_while_typing`

### AzureCommentEndToEndTests

- `A_verdict_posted_as_a_comment_comes_back_rendered`

### TicketAccountsTests

- `The_workspaces_own_choice_wins_over_the_repositorys_link`
- `The_board_project_can_be_chosen_where_the_repository_names_none`
- `The_workspaces_board_project_wins_over_the_repositorys`
- `Clearing_the_board_project_falls_back_to_the_repositorys`
- `Without_a_choice_the_repositorys_own_organisation_is_used`
- `With_exactly_one_connection_that_one_is_the_only_thing_it_could_mean`
- `With_two_connections_and_nothing_chosen_it_refuses_to_guess`
- `With_no_connections_at_all_it_also_refuses`
- `Clearing_the_workspace_choice_falls_back_rather_than_to_nothing`
- `A_blank_choice_counts_as_no_choice`
- `A_malformed_connections_setting_does_not_break_the_module`
- `An_unknown_project_is_a_caller_error_not_an_undecided_account`

### TicketBranchRefTests

- [T] `A_recognised_branch_names_its_ticket`
- [T] `A_branch_with_no_ticket_in_its_name_suggests_nothing`
- `A_prefix_segment_is_not_mistaken_for_the_ticket`
- `A_date_led_branch_is_the_accepted_false_positive`

### TicketCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- `The_only_verb_here_that_writes_to_a_board_is_the_comment`

### TicketCommentTests

- `The_two_verdict_headings_become_headings`
- `Bold_and_inline_code_survive_as_themselves`
- `Two_bold_runs_on_one_line_stay_two`
- `A_diff_quoted_in_the_verdict_cannot_close_a_tag`
- `The_footers_rule_becomes_a_rule`
- `A_bullet_keeps_its_bullet`
- `Blank_lines_are_dropped_rather_than_becoming_empty_boxes`
- `Windows_line_endings_do_not_leave_stray_carriage_returns`

### TicketCriteriaReaderTests

- `A_criteria_field_holding_a_hyphen_is_skipped_for_the_description`
- `A_field_repeated_word_for_word_across_tickets_is_a_form_not_an_answer`
- `With_no_other_ticket_to_compare_against_nothing_is_called_a_template`
- [T] `A_field_below_the_substance_floor_is_not_a_requirement`
- `A_ticket_with_nothing_usable_says_none_rather_than_inventing_criteria`
- `An_explicit_list_is_numbered_deterministically`
- `A_nested_bullet_extends_the_criterion_above_it_instead_of_becoming_its_own`
- `Prose_is_left_whole_with_no_items_to_number`
- `The_configured_order_decides_which_field_wins`
- `A_field_that_is_not_a_string_is_stepped_over_rather_than_crashing`
- `The_default_order_prefers_acceptance_criteria_when_it_is_actually_filled_in`

### TicketHtmlTests

- `A_field_holding_only_a_dash_measures_as_one_character_not_nineteen`
- [T] `Markup_without_content_measures_as_empty`
- `Plain_text_keeps_words_apart_across_block_boundaries`
- `Entities_are_resolved_including_the_non_breaking_space_the_editor_emits`
- `A_less_than_sign_in_prose_does_not_swallow_the_rest_of_the_field`
- `A_nested_list_keeps_its_levels`
- `An_ordered_list_numbers_itself`
- `Bold_and_italic_survive_and_styling_only_markup_does_not`
- `A_link_becomes_markdown_with_its_text_intact`
- `A_link_with_no_text_is_dropped_rather_than_left_as_an_empty_pair_of_brackets`
- `An_image_keeps_its_source_for_the_mirror_to_rewrite`
- `A_tag_whose_attribute_value_contains_a_closing_bracket_is_still_one_tag`
- `Sixty_nested_divs_do_not_become_sixty_blank_lines`
- `Nothing_in_nothing_out`
- `Unbalanced_markup_does_not_throw`
- `A_real_description_converts_to_readable_markdown`

### TicketMirrorTests

- `Anything_the_user_put_in_the_directory_survives_a_resync`
- `The_notes_directory_is_created_once_and_never_filled`
- `Every_derived_file_is_replaced_not_appended_to`
- `The_ticket_page_carries_what_a_person_needs_to_recognise_it`
- `A_ticket_with_no_criteria_says_so_instead_of_leaving_a_blank_section`
- `An_explicit_list_is_written_as_numbered_criteria`
- `Prose_criteria_are_written_whole_and_labelled_as_prose`
- `An_image_is_saved_and_the_markdown_points_at_the_local_copy`
- `Two_attachments_sharing_a_name_both_survive`
- `An_attachment_removed_from_the_ticket_stops_being_mirrored`
- `The_users_notes_are_read_back_for_the_review_to_see`
- `Reading_the_notes_never_writes_to_them`
- `Notes_are_cut_at_their_budget_so_one_long_note_cannot_crowd_out_the_diff`
- `A_ticket_with_no_notes_directory_reads_as_nothing`
- `An_attachment_that_could_not_be_downloaded_is_named_rather_than_omitted`

### TicketPathsTests

- `With_no_setting_the_root_is_the_one_under_the_app_directory`
- `A_configured_root_wins`
- `Clearing_the_field_means_the_default_rather_than_an_empty_path`
- `A_ticket_directory_is_org_then_project_then_the_id_and_title`
- `The_directory_reads_like_the_title_it_came_from`
- `A_ticket_with_no_usable_title_still_gets_a_directory`
- `A_ticket_seen_for_the_first_time_gets_a_directory_from_its_title`
- `A_renamed_ticket_keeps_the_directory_it_already_has`
- `A_blank_stored_path_counts_as_never_mirrored`
- [T] `Slugging_reduces_text_to_one_safe_segment`
- `A_very_long_title_is_cut_without_a_trailing_separator`

### TicketReviewStoreTests

- `A_review_round_trips_with_its_criteria_and_its_coverage`
- `The_coverage_word_is_indexed_on_its_own_column`
- `A_review_the_model_left_without_a_coverage_block_is_still_stored`
- `A_row_whose_payload_will_not_parse_still_renders_its_markdown`
- `Only_this_branchs_reviews_come_back`

### TicketStoreTests

- `A_ticket_round_trips_through_the_cache`
- `Re_syncing_updates_the_row_instead_of_adding_one`
- `A_branch_points_at_one_ticket_and_relinking_replaces_it`
- `An_unlinked_branch_has_no_ticket`
- `A_repository_lists_the_tickets_it_has_linked`
- `Another_repositorys_tickets_are_not_this_ones`
- `A_link_outlives_the_branch_it_names`
- `A_ticket_worked_on_from_two_branches_is_one_entry_with_two_links`
- `Every_link_carries_the_repositorys_name_not_only_its_id`
- `Others_of_the_same_type_are_what_a_template_is_recognised_against`

### TicketVerdictTests

- `The_two_slices_are_disjoint`
- `A_review_without_the_section_is_returned_whole`
- `Every_criterion_comes_back_with_its_verdict_evidence_and_confidence`
- `A_ticket_that_does_not_describe_the_change_says_so`
- `A_review_that_never_answered_the_relevance_question_counts_as_relevant`
- `The_coverage_block_joins_a_summary_that_wrapped`
- [T] `A_verdict_word_is_normalised_towards_caution`
- `A_ticket_with_no_criteria_still_yields_a_coverage_verdict`
- `The_criteria_survive_a_coverage_block_that_never_arrived`
- `ParseFindings_reads_the_same_findings_with_or_without_the_verdict_section`

## Update

### ReleaseVersionTests

- [T] `A_higher_version_is_newer`
- `Ten_is_newer_than_two`
- [T] `The_tag_prefix_is_not_part_of_the_version`
- `A_missing_segment_counts_as_zero`
- `A_prerelease_is_older_than_the_release_it_precedes`
- `A_prerelease_still_beats_an_older_release`
- `Build_metadata_does_not_change_the_answer`
- `A_tag_nobody_here_chose_the_shape_of_does_not_throw`

### UpdateAssetTests

- `The_windows_installer_wins_over_the_portable_build`
- `An_older_release_without_the_marker_is_still_offered`
- `A_sha256_file_is_never_chosen`
- `Each_platform_is_offered_its_own_installer`
- `A_blockmap_is_never_mistaken_for_an_installer`
- `A_release_without_this_platform_offers_nothing`
- `Only_windows_claims_it_can_install_on_its_own`
- `A_github_release_payload_deserialises_by_its_own_names`
- `An_unavailable_answer_carries_why_and_the_running_version`

### UpdateDigestTests

- `The_digest_is_read_from_a_plain_line`
- `A_binary_marker_is_not_part_of_the_name`
- `A_recorded_directory_is_not_part_of_the_name`
- `A_name_with_spaces_survives`
- `A_single_entry_file_is_trusted_without_matching_the_name`
- `The_right_line_is_picked_out_of_several`
- `A_multi_entry_file_that_does_not_list_the_asset_yields_nothing`
- `An_empty_file_yields_nothing`
- `A_name_that_only_looks_similar_does_not_match`

### UpdateDownloadTests

- `An_artefact_matching_its_digest_is_handed_over`
- `An_artefact_that_does_not_match_its_digest_is_refused_and_deleted`
- `A_release_that_publishes_no_digest_is_refused_before_anything_is_downloaded`
- `A_digest_file_that_does_not_list_the_asset_is_refused`
- `The_digest_is_fetched_with_the_same_credential_as_the_artefact`
- `The_digest_is_read_from_the_release_rather_than_from_the_caller`

## Workspaces

### McpConfigTests

- `A_workspace_with_no_enabled_server_produces_no_file_and_no_flag`
- `Only_the_enabled_servers_are_written`
- `The_argument_line_is_split_on_whitespace_and_the_env_on_the_first_equals`
- `An_empty_argument_or_env_line_yields_an_empty_map_rather_than_a_missing_key`
- `Rewriting_replaces_the_previous_file_rather_than_merging_into_it`

### ProjectStoreTests

- `A_created_project_reads_back_exactly_as_it_was_returned`
- `Getting_an_unknown_project_is_null_rather_than_an_error`
- `Moving_a_project_takes_its_review_history_along`
- `Moving_a_project_to_a_workspace_that_does_not_exist_fails`
- `Deleting_a_project_cascades_to_its_own_rows`

### SettingsTests

- `An_unset_key_reads_as_null`
- `A_stored_empty_value_is_a_real_row_and_reads_back_as_an_empty_string`
- `Writing_a_key_twice_updates_it_in_place`
- [T] `Every_prompt_kind_except_sdd_stages_has_non_empty_built_in_text`
- `The_ticket_review_standard_does_not_fall_through_to_the_pr_one`
- `A_new_workspace_is_seeded_with_the_ticket_review_standard`
- `The_built_in_for_an_unrecognised_kind_is_the_review_methodology`
- `The_sdd_stages_built_in_is_empty`
- `Saving_a_blank_prompt_restores_the_built_in_without_deleting_the_row`
- `A_whitespace_only_prompt_counts_as_blank`
- `A_workspace_with_no_row_for_a_kind_falls_through_to_the_built_in`

### SkillCommandsTests

- `The_commands_this_slice_owns_are_registered_under_their_contract_names`
- [T] `A_command_missing_its_argument_names_the_one_it_wanted`
- `Toggling_a_skill_wants_a_boolean_and_says_so`
- `Removing_a_skill_that_does_not_exist_says_so`
- `A_roster_crosses_the_wire_under_the_field_names_the_renderer_reads`
- `An_empty_roster_answers_an_empty_list`
- `The_installer_reaches_npx_the_way_each_platform_needs`
- `An_undeletable_folder_fails_the_remove_and_keeps_the_row_for_a_retry`
- `Installing_over_an_existing_skill_name_is_refused_before_npx_runs`

### SkillFilesTests

- [T] `A_path_that_could_leave_the_skill_is_refused`
- `A_file_can_be_written_read_listed_and_deleted_inside_a_skill`
- `Writing_a_nested_file_creates_the_directories_it_needs`
- `A_skill_that_does_not_exist_lists_nothing_rather_than_failing`
- `A_custom_skill_is_a_folder_with_the_markdown_the_user_wrote`
- `A_custom_skill_needs_a_name_that_survives_sanitising`
- `Creating_over_an_existing_skill_is_refused`
- `Importing_a_folder_copies_it_in_under_its_own_name`
- `A_folder_with_no_skill_markdown_is_not_a_skill`
- [T] `A_name_is_reduced_to_one_usable_path_segment`

### SkillStoreTests

- `An_installed_skill_is_listed_in_installation_order_and_starts_enabled`
- `A_skill_can_be_switched_off_without_being_removed`
- `Skills_are_scoped_to_their_workspace`
- `Deleting_a_workspace_takes_its_skills_with_it`
- `Installing_the_same_skill_twice_leaves_two_rows_over_one_folder`
- `Removing_a_folder_deletes_it_and_frees_its_name`
- `Removing_a_skill_that_left_no_folder_behind_is_still_a_clean_removal`

### SkillSyncTests

- `An_enabled_skill_is_copied_into_the_project`
- `A_whole_skill_tree_comes_across_not_just_its_markdown`
- `A_disabled_skill_is_removed_from_the_project_on_the_next_sync`
- `A_folder_this_workspace_does_not_know_about_is_never_touched`
- `A_skill_in_the_store_with_no_row_is_not_copied`
- `An_empty_roster_creates_nothing_in_the_project`
- `Disabling_a_skill_that_was_never_synced_is_not_an_error`
- `A_re_sync_overwrites_what_it_wrote_before`

### UpsertTests

- `A_null_id_mints_a_new_row_each_time`
- `Editing_a_review_context_keeps_the_stored_creation_time_but_returns_a_fresh_one`
- `Review_contexts_are_listed_in_insertion_order`
- `Editing_an_agent_keeps_its_sort_order_and_creation_time`
- `A_caller_supplied_id_that_matches_no_row_inserts_it_with_sort_order_zero`
- `The_agent_roster_starts_empty`
- `An_mcp_servers_args_and_env_survive_a_round_trip_verbatim`
- `Deleting_removes_only_the_targeted_row`

### WorkspaceCommandsTests

- `All_twenty_seven_commands_are_registered_under_their_contract_names`

### WorkspaceIpcTests

- `A_created_workspace_crosses_the_wire_in_snake_case`
- `A_project_round_trips_through_its_nested_snake_case_input`
- `An_unknown_project_resolves_to_null_rather_than_an_error`
- `A_setting_stored_blank_comes_back_as_an_empty_string_not_null`
- `Saving_a_blank_prompt_restores_the_built_in`
- `An_upsert_with_a_null_id_mints_one`
- `A_failing_command_reaches_the_client_as_an_error_string`

### WorkspaceStoreTests

- `Creating_a_workspace_seeds_both_editable_prompts`
- `Workspaces_are_listed_by_sort_order_then_creation_time`
- `Deleting_a_workspace_cascades_to_everything_scoped_to_it`
- `Updating_a_workspace_colour_leaves_its_other_columns_alone`
- `Renaming_a_workspace_leaves_its_other_columns_alone`
- `Renaming_trims_the_name`
- [T] `A_blank_rename_is_refused_and_the_old_name_survives`
- `A_git_identity_override_round_trips_and_clears_as_a_pair`
- `Resolving_an_identity_finds_the_workspace_through_the_project_path`
- `Resolving_without_an_override_or_a_registered_project_yields_nulls`
- `Two_projects_sharing_a_path_resolve_to_one_workspace_rather_than_failing`
