# Prompt thesaurus reference

Generated from `music-concepts/v3`: **293 concepts and 726 canonical terms/aliases**.

Regenerate with `node scripts/render-music-thesaurus.mjs`; verify with `--check`.

These are contextual vocabulary mappings, not guarantees of musical fit. The parser preserves original source spans, polarity, strength, alternatives and journey scope. Unknown wording remains available to the language model and CLAP; it is not replaced with a guessed parent.

## Provider contracts

AcousticBrainz is an archive of recording analysis, not a prompt encoder. Its legacy classifier labels are checked against a [public high-level document](https://acousticbrainz.org/ac2bbf32-0f6a-48bf-95d5-5edf4d060feb/high-level/view?n=0) and the [Essentia legacy classifier documentation](https://essentia.upf.edu/gaia_svm_models.html). Newer Essentia neural-model labels must not be substituted into this archive schema.

CLAP embeds natural-language audio descriptions. The [upstream implementation](https://github.com/LAION-AI/CLAP) demonstrates text/audio embedding and contextual genre prompts. Captions below are project-authored proposals, not an upstream whitelist or measured optimal prompts. The current uncalibrated best-available query policy averages the canonical phrase and one caption. Strict clauses and calibrated policies retain their existing raw-query contract; changing those requires recalibration.

A dash in the AcousticBrainz column means no exact supported mapping. Missing records, incompatible versions and missing classes remain unknown. A classifier margin and CLAP cosine use different scales and neither is a probability of satisfying the prompt.

## Archived classifier schema

| Classifier | Source labels |
| --- | --- |
| genre_dortmund | alternative, blues, electronic, folkcountry, funksoulrnb, jazz, pop, raphiphop, rock |
| genre_rosamerica | cla, dan, hip, jaz, pop, rhy, roc, spe |
| genre_tzanetakis | blu, cla, cou, dis, hip, jaz, met, pop, reg, roc |
| danceability | danceable, not_danceable |
| mood_acoustic | acoustic, not_acoustic |
| mood_aggressive | aggressive, not_aggressive |
| mood_electronic | electronic, not_electronic |
| mood_happy | happy, not_happy |
| mood_party | party, not_party |
| mood_relaxed | relaxed, not_relaxed |
| mood_sad | sad, not_sad |
| timbre | bright, dark |
| tonal_atonal | tonal, atonal |
| voice_instrumental | voice, instrumental |

Combined Dortmund classes are not aliases for their narrower members: `folkcountry` cannot prove folk or country, `funksoulrnb` cannot prove funk, soul or R&B, and `raphiphop` is not treated as a direct synonym. The [archive label display](https://acousticbrainz.org/a818fa37-5e4d-4245-80aa-d4093eb142ef) identifies ROSAmerica `rhy` as rhythm and blues. Unmapped schema labels remain unused.

| Disabled classifier | Reason |
| --- | --- |
| genre_electronic | Conditional ambient/dnb/house/techno/trance classification needs independent domain applicability; not direct evidence of a subgenre. |
| ismir04_rhythm | Ballroom classifier is not retained by the application projection. |
| moods_mirex | Cluster labels combine different emotions; not exact synonyms. Not retained by the application projection. |
| gender | Demographic classifier deliberately excluded from application projection. |

## Numerical and ambiguous language

| User wording | Interpretation |
| --- | --- |
| 120 BPM, between 100 and 120 BPM | Preserve explicit numerical intent; archive `rhythm.bpm` is a measurement. The thesaurus does not convert a CLAP caption into a verified BPM result. |
| fast, slow, upbeat | Context is needed: tempo, mood and rhythmic feel differ. Do not invent numerical BPM thresholds. |
| loud, energetic, dynamic | Loudness, perceived energy and dynamic variation differ. `lowlevel.average_loudness` and `lowlevel.dynamic_complexity` do not prove the other meanings. |
| danceable | Uses the high-level danceability class. Low-level DFA danceability is a separate numerical scale, not a 0–1 probability. |
| minor key, major key | Tonal key/scale are measurements; do not infer sadness or happiness. |
| dark mood / dark timbre | Emotional darkness stays CLAP-only; spectral darkness maps to timbre.dark. Bare dark keeps the existing mood-first parser behavior. |
| bright mood / bright timbre | Use explicit cheerful/joyful wording for mood. Bare bright keeps the existing timbre interpretation; it does not prove happiness. |
| acoustic guitar / acoustic sound | Guitar identity remains an instrument request. mood_acoustic supports acoustic character, not the presence of a particular instrument. |
| electronic / electronica / electronic production | Genre, narrower genre and production character remain separate. mood_electronic is only production evidence. |
| no vocals / no screaming | Preserve distinct negated clauses; the second does not exclude all singing. No synonym replacement erases no, less, mostly, or stage boundaries. |
| not happy / sad | Not-happy is a classifier complement, not a synonym for sadness. Score polarity is handled outside the dictionary. |
| dissonant / atonal | Dissonance can occur in tonal music. Only explicit atonal terminology maps to tonal_atonal.atonal. |
| dreamy, nostalgic, lo-fi, piano, workout | CLAP descriptions with no direct archive class; do not map to relaxed, sad, acoustic, instrumental or aggressive by association. |
| artist or song names, quoted titles | Keep in the identity resolver and protected source spans, not lexical substitution. |

## Full vocabulary

Aliases are matched case-insensitively within their typed facet. The parser uses token boundaries and prefers longer phrases, so specific subgenres are retained. Provider mapping never expands parent or related concepts. English is the authored language; a few conventional accented spellings are included, not general multilingual coverage.

### genre

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| classical | classical music | Music in the style of classical. | genre_rosamerica:cla; genre_tzanetakis:cla |
| electronic | electronic music | Music in the style of electronic. | genre_dortmund:electronic |
| electronica | — | Music in the style of electronica. | — |
| rock | — | Music in the style of rock. | genre_dortmund:rock; genre_rosamerica:roc; genre_tzanetakis:roc |
| rock & roll | rock and roll; rock n roll; rock 'n' roll | Music in the style of rock & roll. | — |
| hard rock | hard-rock | Music in the style of hard rock. | — |
| industrial rock | industrial-rock | Music in the style of industrial rock. | — |
| industrial | — | Music in the style of industrial. | — |
| industrial metal | industrial-metal | Music in the style of industrial metal. | — |
| metal | — | Music in the style of metal. | genre_tzanetakis:met |
| blues | — | Music in the style of blues. | genre_dortmund:blues; genre_tzanetakis:blu |
| blues rock | blues-rock | Music in the style of blues rock. | — |
| jazz | — | Music in the style of jazz. | genre_dortmund:jazz; genre_rosamerica:jaz; genre_tzanetakis:jaz |
| pop | — | Music in the style of pop. | genre_dortmund:pop; genre_rosamerica:pop; genre_tzanetakis:pop |
| alternative | — | Music in the style of alternative. | genre_dortmund:alternative |
| alternative rock | alternative-rock | Music in the style of alternative rock. | — |
| indie rock | indie-rock | Music in the style of indie rock. | — |
| post-rock | post rock | Music in the style of post-rock. | — |
| hip hop | hip-hop; hiphop; hip hop music | Music in the style of hip hop. | genre_rosamerica:hip; genre_tzanetakis:hip |
| country | — | Music in the style of country. | genre_tzanetakis:cou |
| folk | — | Music in the style of folk. | — |
| soul | — | Music in the style of soul. | — |
| disco | — | Music in the style of disco. | genre_tzanetakis:dis |
| reggae | — | Music in the style of reggae. | genre_tzanetakis:reg |
| ambient | — | Music in the style of ambient. | — |
| ambient electronic | — | Music in the style of ambient electronic. | — |
| ambient electronica | — | Music in the style of ambient electronica. | — |
| dark ambient | dark-ambient | Music in the style of dark ambient. | — |
| downtempo | down-tempo | Music in the style of downtempo. | — |
| house | — | Music in the style of house. | — |
| deep house | deep-house | Music in the style of deep house. | — |
| techno | — | Music in the style of techno. | — |
| melodic techno | — | Music in the style of melodic techno. | — |
| trance | — | Music in the style of trance. | — |
| drum and bass | drum & bass; drum n bass; drum'n'bass; dnb; d&b; drum-n-bass; drum and bass music | Music in the style of drum and bass. | — |
| dubstep | dub-step | Music in the style of dubstep. | — |
| idm | intelligent dance music | Music in the style of idm. | — |
| synth-pop | synthpop; synth pop; synthesized pop | Music in the style of synth-pop. | — |
| trip hop | trip-hop; triphop | Music in the style of trip hop. | — |
| easy listening | easy-listening | Music in the style of easy listening. | — |
| film score | film scores; movie score; movie scores; motion picture score; cinema score | Music in the style of film score. | — |
| ballad | ballads | Music in the style of ballad. | — |
| slow ballad | slow ballads | Music in the style of slow ballad. | — |
| baroque | — | Music in the style of baroque. | — |
| romantic classical | — | Music in the style of romantic classical. | — |
| contemporary classical | — | Music in the style of contemporary classical. | — |
| chamber music | chamber ensemble music | Music in the style of chamber music. | — |
| rhythm and blues | r&b; rnb; rhythm & blues | Music in the style of rhythm and blues. | genre_rosamerica:rhy |
| funk | funk music | Music in the style of funk. | — |
| rap | rap music | Music in the style of rap. | — |
| punk rock | punk-rock | Music in the style of punk rock. | — |
| post-punk | post punk | Music in the style of post-punk. | — |
| synthwave | synth wave | Music in the style of synthwave. | — |
| retrowave | retro wave | Music in the style of retrowave. | — |
| new wave | new-wave | Music in the style of new wave. | — |
| shoegaze | shoe gaze; shoegazing | Music in the style of shoegaze. | — |
| dream pop | dream-pop | Music in the style of dream pop. | — |
| noise rock | noise-rock | Music in the style of noise rock. | — |
| progressive rock | prog rock; prog-rock | Music in the style of progressive rock. | — |
| psychedelic rock | psych rock | Music in the style of psychedelic rock. | — |
| progressive metal | prog metal; prog-metal | Music in the style of progressive metal. | — |
| death metal | death-metal | Music in the style of death metal. | — |
| black metal | black-metal | Music in the style of black metal. | — |
| doom metal | doom-metal | Music in the style of doom metal. | — |
| thrash metal | thrash-metal | Music in the style of thrash metal. | — |
| metalcore | metal core | Music in the style of metalcore. | — |
| grunge | grunge music | Music in the style of grunge. | — |
| post-metal | post metal | Music in the style of post-metal. | — |
| garage rock | garage-rock | Music in the style of garage rock. | — |
| surf rock | surf-rock | Music in the style of surf rock. | — |
| math rock | math-rock | Music in the style of math rock. | — |
| electro | electro music | Music in the style of electro. | — |
| electropop | electro pop; electro-pop | Music in the style of electropop. | — |
| minimal techno | minimal-techno | Music in the style of minimal techno. | — |
| tech house | tech-house | Music in the style of tech house. | — |
| acid house | acid-house | Music in the style of acid house. | — |
| progressive house | progressive-house | Music in the style of progressive house. | — |
| uk garage | ukg; UK garage music | Music in the style of uk garage. | — |
| jungle | jungle music | Music in the style of jungle. | — |
| breakbeat | breakbeats | Music in the style of breakbeat. | — |
| liquid drum and bass | liquid dnb; liquid d&b | Music in the style of liquid drum and bass. | — |
| hardstyle | hard style | Music in the style of hardstyle. | — |
| psytrance | psy trance; psychedelic trance | Music in the style of psytrance. | — |
| future bass | future-bass | Music in the style of future bass. | — |
| chillwave | chill wave | Music in the style of chillwave. | — |
| vaporwave | vapor wave | Music in the style of vaporwave. | — |
| dub | dub music | Music in the style of dub. | — |
| ska | ska music | Music in the style of ska. | — |
| dancehall | dance hall | Music in the style of dancehall. | — |
| reggaeton | reggaetón | Music in the style of reggaeton. | — |
| bossa nova | bossa-nova | Music in the style of bossa nova. | — |
| samba | samba music | Music in the style of samba. | — |
| tango | tango music | Music in the style of tango. | — |
| salsa | salsa music | Music in the style of salsa. | — |
| flamenco | flamenco music | Music in the style of flamenco. | — |
| afrobeat | afrobeat music | Music in the style of afrobeat. | — |
| afrobeats | afrobeats music | Music in the style of afrobeats. | — |
| bluegrass | bluegrass music | Music in the style of bluegrass. | — |
| americana | americana music | Music in the style of americana. | — |
| gospel | gospel music | Music in the style of gospel. | — |
| bebop | be bop; be-bop | Music in the style of bebop. | — |
| free jazz | free-jazz | Music in the style of free jazz. | — |
| jazz fusion | jazz-fusion | Music in the style of jazz fusion. | — |
| smooth jazz | smooth-jazz | Music in the style of smooth jazz. | — |
| swing jazz | swing music | Music in the style of swing jazz. | — |
| neo-soul | neo soul; neosoul | Music in the style of neo-soul. | — |
| contemporary r&b | contemporary rnb; contemporary rhythm and blues | Music in the style of contemporary r&b. | — |
| boom bap | boom-bap | Music in the style of boom bap. | — |
| trap music | trap beats | Music in the style of trap music. | — |
| opera | operatic music | Music in the style of opera. | — |
| minimalist classical | classical minimalism | Music in the style of minimalist classical. | — |
| choral music | choir music | Music in the style of choral music. | — |
| a cappella | a capella; acapella; acappella | Music in the style of a cappella. | — |
| lo-fi hip hop | lofi hip hop; lo-fi hip-hop; lofi hiphop | Music in the style of lo-fi hip hop. | — |

### mood

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| relaxing | relaxed; soothing; relaxational; relaxing mood | Music with a relaxing mood. | mood_relaxed:relaxed |
| aggressive | aggression; aggressive mood; aggressive sounding | Music with an aggressive mood. | mood_aggressive:aggressive |
| happy | cheerful; joyful; joyous; happy sounding | Music with a happy mood. | mood_happy:happy |
| sad | sorrowful; sad sounding; sorrowful mood | Music with a sad mood. | mood_sad:sad |
| party | party music; party atmosphere | Music with a party mood. | mood_party:party |
| energetic | high energy; high-energy; high powered; full of energy; energetic mood | Music with an energetic mood. | — |
| dramatic | — | Music with a dramatic mood. | — |
| melancholic | melancholy; melancholia | Music with a melancholic mood. | — |
| comforting | reassuring | Music with a comforting mood. | — |
| dark | — | Music with a dark mood. | — |
| calm | tranquil; peaceful; serene | Music with a calm mood. | — |
| gentle | gentleness; gentle mood | Music with a gentle mood. | — |
| dreamy | dreamlike; dream-like; dreamy mood | Music with a dreamy mood. | — |
| wistful | wistfulness | Music with a wistful mood. | — |
| romantic | romantic mood | Music with a romantic mood. | — |
| sleepy | drowsy; sleep inducing | Music with a sleepy mood. | — |
| uplifting | uplift; uplifting mood | Music with an uplifting mood. | — |
| meditative | contemplative | Music with a meditative mood. | — |
| intense | — | Music with an intense mood. | — |
| tense | tension filled; tense mood | Music with a tense mood. | — |
| euphoric | euphoria; euphoric mood | Music with an euphoric mood. | — |
| nostalgic | nostalgia; nostalgic mood | Music with a nostalgic mood. | — |
| ominous | foreboding; ominous mood | Music with an ominous mood. | — |
| mysterious | enigmatic; mysterious mood | Music with a mysterious mood. | — |
| playful | playfulness | Music with a playful mood. | — |
| hopeful | hopefulness | Music with a hopeful mood. | — |
| lonely | lonesome; loneliness | Music with a lonely mood. | — |
| triumphant | triumphal; triumphant mood | Music with a triumphant mood. | — |
| bittersweet | bitter sweet; bitter-sweet | Music with a bittersweet mood. | — |
| somber | sombre; somber mood | Music with a somber mood. | — |
| angry | anger; angry mood | Music with an angry mood. | — |
| tender | tenderness | Music with a tender mood. | — |
| restless | restlessness | Music with a restless mood. | — |
| majestic | majestic mood | Music with a majestic mood. | — |
| brooding | brooding mood | Music with a brooding mood. | — |
| haunting | haunting mood | Music with a haunting mood. | — |
| whimsical | whimsy | Music with a whimsical mood. | — |

### instrumentation

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| orchestral | orchestral music | Music played by an orchestra. | — |
| piano | pianos; acoustic piano; piano led; piano-led | Music featuring piano. | — |
| strings | string instruments; string section; string ensemble | Music featuring strings. | — |
| guitar | guitars; guitar led; guitar-led | Music featuring guitar. | — |
| electric guitar | electric guitars; electric guitar led; electric guitar-led | Music featuring electric guitar. | — |
| acoustic guitar | acoustic guitars; acoustic guitar led; acoustic guitar-led | Music featuring acoustic guitar. | — |
| drums | drum kit; drumkit; drum set | Music featuring drums. | — |
| synthesizer | synthesizers; synths; synth; synthesiser; synthesisers | Music featuring synthesizer. | — |
| violin | violins | Music featuring violin. | — |
| cello | cellos; violoncello; violoncellos | Music featuring cello. | — |
| orchestra | — | Music featuring orchestra. | — |
| saxophone | saxophones; sax | Music featuring saxophone. | — |
| bass guitar | electric bass guitar | Music featuring bass guitar. | — |
| flute | flutes | Music featuring flute. | — |
| clarinet | clarinets | Music featuring clarinet. | — |
| oboe | oboes | Music featuring oboe. | — |
| bassoon | bassoons | Music featuring bassoon. | — |
| trumpet | trumpets | Music featuring trumpet. | — |
| trombone | trombones | Music featuring trombone. | — |
| french horn | french horns | Music featuring french horn. | — |
| tuba | tubas | Music featuring tuba. | — |
| harp | harps | Music featuring harp. | — |
| harpsichord | harpsichords | Music featuring harpsichord. | — |
| pipe organ | pipe organs | Music featuring pipe organ. | — |
| electric organ | electric organs | Music featuring electric organ. | — |
| electric piano | electric pianos | Music featuring electric piano. | — |
| double bass | upright bass; string bass | Music featuring double bass. | — |
| viola | violas | Music featuring viola. | — |
| banjo | banjos | Music featuring banjo. | — |
| mandolin | mandolins | Music featuring mandolin. | — |
| ukulele | ukuleles; uke | Music featuring ukulele. | — |
| accordion | accordions | Music featuring accordion. | — |
| harmonica | harmonicas | Music featuring harmonica. | — |
| sitar | sitars | Music featuring sitar. | — |
| tabla | tablas | Music featuring tabla. | — |
| congas | conga drums | Music featuring congas. | — |
| bongos | bongo drums | Music featuring bongos. | — |
| timpani | kettledrums | Music featuring timpani. | — |
| marimba | marimbas | Music featuring marimba. | — |
| vibraphone | vibraphones | Music featuring vibraphone. | — |
| xylophone | xylophones | Music featuring xylophone. | — |
| drum machine | drum machines | Music featuring drum machine. | — |
| brass section | brass ensemble | Music featuring brass section. | — |
| woodwinds | woodwind section; woodwind ensemble | Music featuring woodwinds. | — |
| string quartet | string quartets | Music featuring string quartet. | — |
| choir | choirs; chorus ensemble | Music featuring choir. | — |

### vocal

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| instrumental | instrumental music; instrumentals | Instrumental music without a singing voice. | voice_instrumental:instrumental |
| vocals | vocal; voice; singing; sung vocals; sung voice; vocal singing | Music with vocals. | voice_instrumental:voice |
| screaming | screamed vocals; screamed singing; screams | Music with screamed vocals. | — |
| harsh vocals | harsh singing; harsh vocal delivery | Music with harsh vocals. | — |
| female vocals | female singing | Music with a female singer. | — |
| male vocals | male singing | Music with a male singer. | — |
| whispered vocals | whisper singing; whispered singing | Music with whispered vocals. | — |
| breathy vocals | breathy singing | Music with breathy vocals. | — |
| growled vocals | growling vocals; growled singing | Music with growled vocals. | — |
| clean vocals | clean singing | Music with clean vocals. | — |
| raspy vocals | raspy singing; gravelly vocals | Music with raspy vocals. | — |
| falsetto | falsetto singing | Music with falsetto singing. | — |
| humming | hummed vocals | Music with humming. | — |
| vocal harmonies | harmonized vocals; harmonised vocals | Music with vocal harmonies. | — |
| spoken word | spoken-word; spoken vocals | A spoken word performance over music. | — |
| rapping | rapped vocals; rapped delivery | Music with rapping. | — |
| chanting | chanted vocals | Music with chanting. | — |
| scat singing | scat vocals | Music with scat singing. | — |
| yodeling | yodelling | Music with yodeling. | — |

### texture

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| bright | bright sound; bright timbre; bright tone; bright tonal colour; bright tonal color | Music with a bright timbre. | timbre:bright |
| dark | dark sound; dark timbre; dark tone; dark tonal colour; dark tonal color | Music with a dark timbre. | timbre:dark |
| dynamic | wide dynamics; big dynamic swings; big changes from quiet to loud; wide dynamic range; large dynamic range | Music with wide dynamics, changing from quiet to loud. | — |
| compressed dynamics | narrow dynamic range; compressed dynamic range | Music with a narrow dynamic range. | — |
| bass heavy | bass-heavy; strong bass; bass heavy sound; bass emphasis; bass forward; bass-forward | Music with strong, prominent bass. | — |
| deep bass | deep low bass | Music with deep bass. | — |
| strong sub-bass | strong subbass; sub bass emphasis; sub-bass emphasis | Music with strong sub-bass. | — |
| percussive | lots of transients; sharp attacks; percussion driven; percussion-driven | Music with prominent percussion and sharp rhythmic attacks. | — |
| danceable | easy to dance to; suited to dancing; danceable rhythm | Music with a rhythm suited to dancing. | danceability:danceable |
| tonal | tonal harmony; tonal music | Music with a clear tonal center. | tonal_atonal:tonal |
| atonal | atonality; atonal harmony; atonal music | Atonal music without a clear tonal center. | tonal_atonal:atonal |
| acoustic | acoustic instrumentation; acoustically played; acoustic sound | Music played on acoustic instruments. | mood_acoustic:acoustic |
| electronic production | electronically produced; electronic sound; electronic instrumentation | Music made with electronic instruments and electronic production. | mood_electronic:electronic |
| bluesy | — | Music with blues-influenced phrasing. | — |
| warm | warm textures; warm sound; warm timbre | Music with a warm timbre. | — |
| gentle pulse | — | Music with a soft, steady rhythmic pulse. | — |
| heavy | — | Music with a heavy sound. | — |
| clubby | — | Music with a nightclub dance sound. | — |
| driving | — | Music with a driving rhythmic feel. | — |
| driving guitar riffs | — | Music with driving guitar riffs. | — |
| guitar riffs | guitar riff; riffing guitar | Music with guitar riffs. | — |
| raw live-band feel | raw live band feel | Music with the raw sound of a live band. | — |
| polished pop | — | Music with polished pop production. | — |
| polished | — | Music with polished production. | — |
| steady beat | steady pulse; regular beat | Music with a steady beat. | — |
| microdetail | fine sonic detail | Music with fine audible sonic details. | — |
| sparkle | — | Music with a sparkling sound. | — |
| distorted guitar | distorted guitars; guitar distortion | Music with distorted guitar. | — |
| fuzzy guitar | fuzz guitar; fuzzed guitar | Music with fuzzy guitar. | — |
| clean guitar tone | undistorted guitar; clean guitar sound | Music with a clean, undistorted guitar tone. | — |
| reverberant | reverb laden; reverb-laden; lots of reverb | Music with reverberant sounds and audible reverb tails. | — |
| dry recording | dry production; dry recorded sound | Music recorded with a dry sound and little reverberation. | — |
| lo-fi production | lofi production; low fidelity production; low-fidelity production | Music with lo-fi production. | — |
| crackly recording | vinyl crackle; record crackle | Music with audible recording crackle. | — |
| spacious | spacious sound; spacious production | Music with a spacious recorded sound. | — |
| dense arrangement | densely arranged; dense instrumentation | Music with a dense arrangement. | — |
| sparse arrangement | sparsely arranged; sparse instrumentation | Music with a sparse arrangement. | — |
| minimal arrangement | minimal instrumentation; stripped down arrangement; stripped-down arrangement | Music with a stripped-down arrangement. | — |
| layered | layered sound; layered arrangement | Music with layered sounds. | — |
| syncopated | syncopation; syncopated rhythm | Music with syncopated rhythms. | — |
| polyrhythmic | polyrhythm; polyrhythms | Music with simultaneous contrasting rhythmic patterns. | — |
| swung rhythm | swing rhythm; swung beat | Music with a swung rhythm. | — |
| four on the floor | four-on-the-floor; four to the floor | Music with a four-on-the-floor kick drum rhythm. | — |
| broken beat | broken beats | Music with a broken beat rhythm. | — |
| arpeggiated | arpeggios; arpeggiated pattern | Music with arpeggiated note patterns. | — |
| droning | sustained drone; drone texture | Music with sustained droning tones. | — |
| sustained tones | sustained notes | Music with sustained tones. | — |
| staccato | short detached notes | Music played with short, detached notes. | — |
| legato | smooth connected notes | Music played with smoothly connected notes. | — |
| plucked strings | plucked string sound | Music with plucked strings. | — |
| bowed strings | bowed string sound | Music with bowed strings. | — |
| fingerpicked guitar | fingerpicking; fingerstyle guitar | Music with fingerpicked guitar. | — |
| strummed guitar | strumming; guitar strumming | Music with strummed guitar. | — |
| palm muted guitar | palm-muted guitar | Music with palm muted guitar. | — |
| tremolo | tremolo texture | Music with a tremolo effect. | — |
| glitchy | glitch texture; glitch effects | Music with glitch-like sound effects. | — |
| shimmering | shimmering texture | Music with shimmering textures. | — |
| airy | airy texture | Music with an airy sound. | — |
| gritty | gritty texture | Music with gritty textures. | — |
| punchy drums | punchy percussion | Music with punchy drums. | — |
| rolling bassline | rolling bass line | Music with a rolling bassline. | — |
| sub bass | sub-bass; subbass | Music with low sub bass frequencies. | — |
| walking bass | walking bassline; walking bass line | Music with walking bass. | — |
| melodic | melodic phrasing | Music with prominent melodic phrasing. | — |
| dissonant | dissonance; dissonant harmony | Music with dissonant harmonies. | — |
| consonant | consonance; consonant harmony | Music with consonant harmonies. | — |

### activity

| Canonical term | Accepted aliases | CLAP caption | AcousticBrainz model:class |
| --- | --- | --- | --- |
| focus | concentrate; concentration; focusing; focused work | Music for focus. | — |
| workout | working out; exercise; workouts; fitness training | Music for working out. | — |
| running | jogging | Music for running. | — |
| lifting weights | weightlifting; weight training | Music for lifting weights. | — |
| reading | — | Music for reading. | — |
| studying | study session | Music for studying. | — |
| sleep | sleeping; bedtime | Music for sleep. | — |
| yoga | yoga practice | Music for yoga. | — |
| meditation | meditation session | Music for meditation. | — |
| driving | road trip | Music for driving. | — |
| dinner | dinner music | Music for dinner. | — |

## Validation and remaining evaluation

Offline tests cover schema validity, alias equivalence, provider-specific unknowns, original source spans, negation, protected names and CLAP query-policy boundaries. They establish mapping behavior, not recognition quality on real recordings.

Before claiming a quality improvement, freeze the installed CLAP checkpoint, preprocessing, vocabulary version, corpus and seeds. Compare raw prompt, canonical-only and canonical-plus-caption retrieval on a held-out, artist-disjoint listening set. Include synonyms, near-synonyms, ambiguous words, negative clauses and unseen descriptions. Report per-facet ranking metrics and human relevance judgments, failure rates, and changes for exclusion cases. For AcousticBrainz, evaluate each classifier separately on matched recording IDs and report coverage/conflicts; do not tune new hard thresholds from synonym examples. No model inference or listening-set evaluation was performed to author this table.
